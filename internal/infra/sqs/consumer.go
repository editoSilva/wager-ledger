package sqsinfra

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/config"
	"github.com/editosilva/wager-ledger/internal/domain/money"
)

const consumerName = "wager-transactions"

type envelope struct {
	MessageID string `json:"messageId"`
	Type      string `json:"type"`
	Data      struct {
		ProviderID                     string      `json:"providerId"`
		ExternalTransactionID          string      `json:"externalTransactionId"`
		IdempotencyKey                 string      `json:"idempotencyKey"`
		PlayerID                       string      `json:"playerId"`
		WalletID                       string      `json:"walletId"`
		RoundID                        string      `json:"roundId"`
		GameID                         string      `json:"gameId"`
		Kind                           string      `json:"kind"`
		Money                          money.Money `json:"money"`
		ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId"`
	} `json:"data"`
}

type Consumer struct {
	client   *awssqs.Client
	queueURL string
	inbox    ports.InboxRepository
	uow      ports.UnitOfWork
	process  *usecase.ProcessWagerTransaction
	logger   *slog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewConsumer(lc fx.Lifecycle, cfg config.Config, inbox ports.InboxRepository, uow ports.UnitOfWork, process *usecase.ProcessWagerTransaction, logger *slog.Logger) (*Consumer, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.AWSRegion), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretKey, "")))
	if err != nil {
		return nil, err
	}
	awsCfg.BaseEndpoint = &cfg.SQSEndpoint
	c := &Consumer{client: awssqs.NewFromConfig(awsCfg), queueURL: cfg.SQSQueueURL, inbox: inbox, uow: uow, process: process, logger: logger, done: make(chan struct{})}
	lc.Append(fx.Hook{OnStart: func(context.Context) error {
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		go c.run(ctx)
		return nil
	}, OnStop: func(ctx context.Context) error {
		c.cancel()
		select {
		case <-c.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	return c, nil
}

func (c *Consumer) run(ctx context.Context) {
	defer close(c.done)
	for ctx.Err() == nil {
		out, err := c.client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: &c.queueURL, MaxNumberOfMessages: 10, WaitTimeSeconds: 10})
		if err != nil {
			if ctx.Err() == nil {
				c.logger.Error("falha ao receber SQS", slog.Any("error", err))
				time.Sleep(time.Second)
			}
			continue
		}
		for _, m := range out.Messages {
			if err := c.handle(ctx, m.Body, m.MessageId); err != nil {
				c.logger.Error("falha ao processar mensagem SQS", slog.Any("error", err))
				continue
			}
			_, err = c.client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: &c.queueURL, ReceiptHandle: m.ReceiptHandle})
			if err != nil {
				c.logger.Error("falha ao remover mensagem SQS", slog.Any("error", err))
			}
		}
	}
}

func (c *Consumer) handle(ctx context.Context, body, sqsID *string) error {
	e, hash, err := decodeEnvelope(body)
	if err != nil {
		return err
	}
	id := e.MessageID
	if id == "" && sqsID != nil {
		id = *sqsID
	}
	return c.uow.Execute(ctx, func(txCtx context.Context) error {
		err := c.inbox.Create(txCtx, consumerName, id, hash)
		if errors.Is(err, ports.ErrAlreadyExists) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = c.process.ExecuteWithin(txCtx, usecase.ProcessWagerTransactionInput{ProviderID: e.Data.ProviderID, ExternalTransactionID: e.Data.ExternalTransactionID, IdempotencyKey: e.Data.IdempotencyKey, PlayerID: e.Data.PlayerID, WalletID: e.Data.WalletID, RoundID: e.Data.RoundID, GameID: e.Data.GameID, Kind: e.Data.Kind, Money: e.Data.Money, ReferenceExternalTransactionID: e.Data.ReferenceExternalTransactionID})
		if err != nil {
			return err
		}
		return c.inbox.MarkCompleted(txCtx, consumerName, id)
	})
}

func decodeEnvelope(body *string) (envelope, string, error) {
	var e envelope
	if body == nil {
		return e, "", errors.New("mensagem SQS vazia")
	}
	if err := json.Unmarshal([]byte(*body), &e); err != nil {
		return e, "", err
	}
	if e.Type != "WagerTransactionRequested" || e.MessageID == "" {
		return e, "", errors.New("envelope SQS inválido")
	}
	sum := sha256.Sum256([]byte(*body))
	return e, hex.EncodeToString(sum[:]), nil
}
