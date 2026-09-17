package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/money"
)

func TestOutboxRepository_Create_PersistsPayload(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewOutboxRepository(pool)

	aggregateID := uuid.NewString()
	eventID := uuid.NewString()
	correlationID := uuid.NewString()
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)

	amount, err := money.FromDecimalString("1000.00", "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}

	e := event.NewWagerTransactionProcessed(eventID, aggregateID, correlationID, occurredAt, event.WagerTransactionProcessedData{
		TransactionID: uuid.NewString(),
		WalletID:      aggregateID,
		PlayerID:      uuid.NewString(),
		Kind:          "OPENING",
		Money:         amount,
	})

	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create erro inesperado: %v", err)
	}

	var eventType, payloadRaw string
	var publishedAt *time.Time
	var attempts int
	row := pool.QueryRow(ctx,
		`SELECT event_type, payload::text, published_at, attempts FROM outbox_events WHERE id = $1`,
		eventID,
	)
	if err := row.Scan(&eventType, &payloadRaw, &publishedAt, &attempts); err != nil {
		t.Fatalf("erro ao ler outbox_events: %v", err)
	}

	if eventType != event.TypeWagerTransactionProcessed {
		t.Errorf("event_type = %s, esperado %s", eventType, event.TypeWagerTransactionProcessed)
	}
	if publishedAt != nil {
		t.Errorf("published_at deveria ser NULL para evento recém-criado, got %v", publishedAt)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, esperado 0", attempts)
	}

	var data event.WagerTransactionProcessedData
	if err := json.Unmarshal([]byte(payloadRaw), &data); err != nil {
		t.Fatalf("erro ao decodificar payload: %v", err)
	}
	if data.Kind != "OPENING" {
		t.Errorf("payload.kind = %s, esperado OPENING", data.Kind)
	}
}
