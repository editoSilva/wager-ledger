package outbox

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/infra/postgres"
	"github.com/editosilva/wager-ledger/internal/observability"
)

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func outboxTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := getenvDefault("DATABASE_URL", "postgres://wager:wager@localhost:5432/wager_ledger?sslmode=disable")
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("erro ao conectar no Postgres de teste: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestPublisher(t *testing.T, repo *postgres.OutboxRepository, metrics *observability.Metrics) *Publisher {
	t.Helper()
	endpoint := getenvDefault("SQS_ENDPOINT", "http://localhost:4566")
	queueURL := getenvDefault("EVENTS_QUEUE_URL", "http://localhost:4566/000000000000/wager-events.fifo")

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(getenvDefault("AWS_REGION", "us-east-1")),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			getenvDefault("AWS_ACCESS_KEY_ID", "test"), getenvDefault("AWS_SECRET_ACCESS_KEY", "test"), "")),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	awsCfg.BaseEndpoint = &endpoint

	return &Publisher{
		repo:     repo,
		client:   awssqs.NewFromConfig(awsCfg),
		queueURL: queueURL,
		workerID: uuid.NewString(),
		metrics:  metrics,
		logger:   noopLogger(),
		done:     make(chan struct{}),
	}
}

func createOutboxEvent(t *testing.T, ctx context.Context, repo *postgres.OutboxRepository) string {
	t.Helper()
	aggregateID := uuid.NewString()
	eventID := uuid.NewString()
	amount, err := money.FromDecimalString("10.00", "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}
	e := event.NewWagerTransactionProcessed(eventID, aggregateID, uuid.NewString(), time.Now().UTC(), event.WagerTransactionProcessedData{
		TransactionID: uuid.NewString(), WalletID: aggregateID, PlayerID: uuid.NewString(), Kind: "BET", Money: amount,
	})
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create outbox event: %v", err)
	}
	return eventID
}

// TestTwoPublishers_ContendingForSamePendingRecords_NoDoublePublish cobre o
// item 6 do README §13: dois publishers da outbox (dois processos/instâncias
// de outbox.Publisher) disputando os mesmos registros pendentes no mesmo
// Postgres não devem publicar o mesmo evento duas vezes, graças ao claim com
// lock e TTL (SKIP LOCKED). Roda contra LocalStack real.
func TestTwoPublishers_ContendingForSamePendingRecords_NoDoublePublish(t *testing.T) {
	pool := outboxTestPool(t)
	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	const nEvents = 30
	eventIDs := make(map[string]bool, nEvents)
	for i := 0; i < nEvents; i++ {
		eventIDs[createOutboxEvent(t, ctx, repo)] = true
	}
	t.Cleanup(func() {
		for id := range eventIDs {
			_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id = $1`, id)
		}
	})

	metrics := observability.NewMetrics()
	publisherA := newTestPublisher(t, repo, metrics)
	publisherB := newTestPublisher(t, repo, metrics)

	var wg sync.WaitGroup
	for _, p := range []*Publisher{publisherA, publisherB} {
		wg.Add(1)
		go func(p *Publisher) {
			defer wg.Done()
			// Disputam repetidamente as mesmas rodadas de claim para maximizar a
			// chance de colisão em torno dos mesmos registros pendentes.
			for i := 0; i < 5; i++ {
				p.publishPending(ctx)
			}
		}(p)
	}
	wg.Wait()

	// Todos os eventos devem terminar publicados (published_at preenchido) e
	// nenhum deve ter sido reivindicado por dois workers simultaneamente ao
	// ponto de ser publicado mais de uma vez (idempotência garantida por
	// MessageDeduplicationId=eventId, e ausência de corrida graças ao SKIP LOCKED).
	var publishedCount int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE id = ANY($1) AND published_at IS NOT NULL`, idsSlice(eventIDs))
	if err := row.Scan(&publishedCount); err != nil {
		t.Fatalf("erro ao contar outbox_events publicados: %v", err)
	}
	if publishedCount != nEvents {
		t.Errorf("published_at preenchido em %d/%d eventos, esperado todos publicados", publishedCount, nEvents)
	}

	receivedIDs := map[string]int{}
	queueURL := getenvDefault("EVENTS_QUEUE_URL", "http://localhost:4566/000000000000/wager-events.fifo")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && len(receivedIDs) < nEvents {
		out, err := publisherA.client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl: &queueURL, MaxNumberOfMessages: 10, WaitTimeSeconds: 2, VisibilityTimeout: 1,
		})
		if err != nil {
			t.Fatalf("ReceiveMessage: %v", err)
		}
		for _, m := range out.Messages {
			id, _, err := decodeOutboxEnvelope(*m.Body)
			if err != nil {
				t.Fatalf("decodeOutboxEnvelope: %v", err)
			}
			if eventIDs[id] {
				receivedIDs[id]++
			}
			_, _ = publisherA.client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: &queueURL, ReceiptHandle: m.ReceiptHandle})
		}
	}

	for id, count := range receivedIDs {
		if count > 1 {
			t.Errorf("evento %s recebido %d vezes na fila, esperado no máximo 1 (sem publicação duplicada)", id, count)
		}
	}
	if len(receivedIDs) != nEvents {
		t.Errorf("recebidos %d/%d eventos únicos na fila", len(receivedIDs), nEvents)
	}
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func decodeOutboxEnvelope(body string) (id, eventType string, err error) {
	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return "", "", err
	}
	return env.EventID, env.EventType, nil
}

func idsSlice(m map[string]bool) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	return ids
}
