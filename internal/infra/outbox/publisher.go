package outbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/config"
	"github.com/editosilva/wager-ledger/internal/observability"
)

const (
	lockTTL        = 30 * time.Second
	pollInterval   = time.Second
	claimBatchSize = 20
	baseBackoff    = time.Second
	maxBackoff     = 5 * time.Minute
)

type envelope struct {
	EventID       string          `json:"eventId"`
	EventType     string          `json:"eventType"`
	AggregateID   string          `json:"aggregateId"`
	CorrelationID string          `json:"correlationId,omitempty"`
	CausationID   string          `json:"causationId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Version       int             `json:"version"`
	Data          json.RawMessage `json:"data"`
}

type Publisher struct {
	repo     ports.OutboxRepository
	client   *awssqs.Client
	queueURL string
	workerID string
	metrics  *observability.Metrics
	logger   *slog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewPublisher(lc fx.Lifecycle, cfg config.Config, repo ports.OutboxRepository, metrics *observability.Metrics, logger *slog.Logger) (*Publisher, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	awsCfg.BaseEndpoint = &cfg.SQSEndpoint

	p := &Publisher{
		repo:     repo,
		client:   awssqs.NewFromConfig(awsCfg),
		queueURL: cfg.EventsQueueURL,
		workerID: uuid.NewString(),
		metrics:  metrics,
		logger:   logger,
		done:     make(chan struct{}),
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			p.cancel = cancel
			go p.run(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			p.cancel()
			select {
			case <-p.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return p, nil
}

func (p *Publisher) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		p.publishPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Publisher) publishPending(ctx context.Context) {
	records, err := p.repo.Claim(ctx, p.workerID, claimBatchSize, lockTTL)
	if err != nil {
		if ctx.Err() == nil {
			p.logger.Error("falha ao reivindicar eventos da outbox", slog.Any("error", err))
		}
		return
	}
	for _, rec := range records {
		if ctx.Err() != nil {
			return
		}
		p.publishOne(ctx, rec)
	}
}

func (p *Publisher) publishOne(ctx context.Context, rec ports.OutboxRecord) {
	env := envelope{
		EventID:       rec.ID,
		EventType:     rec.EventType,
		AggregateID:   rec.AggregateID,
		CorrelationID: rec.CorrelationID,
		CausationID:   rec.CausationID,
		OccurredAt:    rec.OccurredAt.UTC(),
		Version:       rec.Version,
		Data:          rec.Payload,
	}

	body, err := json.Marshal(env)
	if err != nil {
		p.logger.Error("falha ao serializar evento da outbox", slog.String("eventId", rec.ID), slog.Any("error", err))
		p.markFailed(ctx, rec)
		return
	}

	groupID := rec.AggregateID
	dedupID := rec.ID
	bodyStr := string(body)
	_, err = p.client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               &p.queueURL,
		MessageBody:            &bodyStr,
		MessageGroupId:         &groupID,
		MessageDeduplicationId: &dedupID,
	})
	if err != nil {
		p.logger.Error("falha ao publicar evento da outbox", slog.String("eventId", rec.ID), slog.Any("error", err))
		p.markFailed(ctx, rec)
		return
	}

	if err := p.repo.MarkPublished(ctx, rec.ID); err != nil {
		p.logger.Error("falha ao confirmar publicação na outbox", slog.String("eventId", rec.ID), slog.Any("error", err))
		return
	}

	if p.metrics != nil {
		p.metrics.OutboxPublishLatencySeconds.Observe(time.Since(rec.OccurredAt).Seconds())
	}
}

func (p *Publisher) markFailed(ctx context.Context, rec ports.OutboxRecord) {
	if p.metrics != nil {
		p.metrics.OutboxPublishFailuresTotal.Inc()
	}
	next := time.Now().Add(backoff(rec.Attempts))
	if err := p.repo.MarkFailed(ctx, rec.ID, next); err != nil {
		p.logger.Error("falha ao registrar tentativa da outbox", slog.String("eventId", rec.ID), slog.Any("error", err))
	}
}

func backoff(attempts int) time.Duration {
	d := time.Duration(math.Pow(2, float64(attempts))) * baseBackoff
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}
