package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/money"
)

func createTestOutboxEvent(t *testing.T, ctx context.Context, repo *OutboxRepository) string {
	t.Helper()
	aggregateID := uuid.NewString()
	eventID := uuid.NewString()
	amount, err := money.FromDecimalString("10.00", "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}
	e := event.NewWagerTransactionProcessed(eventID, aggregateID, uuid.NewString(), time.Now().UTC(), event.WagerTransactionProcessedData{
		TransactionID: uuid.NewString(),
		WalletID:      aggregateID,
		PlayerID:      uuid.NewString(),
		Kind:          "OPENING",
		Money:         amount,
	})
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create erro inesperado: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id = $1`, eventID); err != nil {
			t.Logf("falha ao limpar outbox_events de teste: %v", err)
		}
	})
	return eventID
}

const claimBatchLimitForTest = 1000

func TestOutboxRepository_Claim_LocksAgainstOtherWorkers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewOutboxRepository(pool)

	eventID := createTestOutboxEvent(t, ctx, repo)

	recordsA, err := repo.Claim(ctx, "worker-a", claimBatchLimitForTest, time.Minute)
	if err != nil {
		t.Fatalf("Claim(worker-a) erro inesperado: %v", err)
	}
	if !containsRecord(recordsA, eventID) {
		t.Fatalf("worker-a deveria reivindicar o evento %s", eventID)
	}

	recordsB, err := repo.Claim(ctx, "worker-b", claimBatchLimitForTest, time.Minute)
	if err != nil {
		t.Fatalf("Claim(worker-b) erro inesperado: %v", err)
	}
	if containsRecord(recordsB, eventID) {
		t.Fatalf("worker-b não deveria conseguir reivindicar evento já travado por worker-a")
	}
}

func TestOutboxRepository_Claim_ReclaimsExpiredLock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewOutboxRepository(pool)

	eventID := createTestOutboxEvent(t, ctx, repo)

	if _, err := repo.Claim(ctx, "worker-a", claimBatchLimitForTest, 0); err != nil {
		t.Fatalf("Claim(worker-a) erro inesperado: %v", err)
	}

	records, err := repo.Claim(ctx, "worker-b", claimBatchLimitForTest, 0)
	if err != nil {
		t.Fatalf("Claim(worker-b) erro inesperado: %v", err)
	}
	if !containsRecord(records, eventID) {
		t.Fatalf("worker-b deveria reivindicar o evento após o lock expirar")
	}
}

func TestOutboxRepository_MarkPublished_ExcludesFromFutureClaims(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewOutboxRepository(pool)

	eventID := createTestOutboxEvent(t, ctx, repo)

	if _, err := repo.Claim(ctx, "worker-a", claimBatchLimitForTest, time.Minute); err != nil {
		t.Fatalf("Claim erro inesperado: %v", err)
	}
	if err := repo.MarkPublished(ctx, eventID); err != nil {
		t.Fatalf("MarkPublished erro inesperado: %v", err)
	}

	var publishedAt *time.Time
	row := pool.QueryRow(ctx, `SELECT published_at FROM outbox_events WHERE id = $1`, eventID)
	if err := row.Scan(&publishedAt); err != nil {
		t.Fatalf("erro ao ler outbox_events: %v", err)
	}
	if publishedAt == nil {
		t.Fatal("published_at deveria estar preenchido após MarkPublished")
	}

	records, err := repo.Claim(ctx, "worker-b", claimBatchLimitForTest, 0)
	if err != nil {
		t.Fatalf("Claim erro inesperado: %v", err)
	}
	if containsRecord(records, eventID) {
		t.Fatalf("evento publicado não deveria ser reivindicável novamente")
	}
}

func TestOutboxRepository_MarkFailed_IncrementsAttemptsAndReleasesLock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewOutboxRepository(pool)

	eventID := createTestOutboxEvent(t, ctx, repo)

	if _, err := repo.Claim(ctx, "worker-a", claimBatchLimitForTest, time.Minute); err != nil {
		t.Fatalf("Claim erro inesperado: %v", err)
	}
	if err := repo.MarkFailed(ctx, eventID, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("MarkFailed erro inesperado: %v", err)
	}

	var attempts int
	var lockedBy *string
	row := pool.QueryRow(ctx, `SELECT attempts, locked_by FROM outbox_events WHERE id = $1`, eventID)
	if err := row.Scan(&attempts, &lockedBy); err != nil {
		t.Fatalf("erro ao ler outbox_events: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, esperado 1", attempts)
	}
	if lockedBy != nil {
		t.Errorf("locked_by deveria ser NULL após MarkFailed, got %v", *lockedBy)
	}

	records, err := repo.Claim(ctx, "worker-b", claimBatchLimitForTest, time.Minute)
	if err != nil {
		t.Fatalf("Claim erro inesperado: %v", err)
	}
	if !containsRecord(records, eventID) {
		t.Fatalf("evento com next_attempt_at no passado deveria ser reivindicável de novo")
	}
}

func containsRecord(records []ports.OutboxRecord, id string) bool {
	for _, r := range records {
		if r.ID == id {
			return true
		}
	}
	return false
}
