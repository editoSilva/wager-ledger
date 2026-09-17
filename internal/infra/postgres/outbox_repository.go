package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/event"
)

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

func (r *OutboxRepository) Create(ctx context.Context, e event.Event) error {
	q := querierFrom(ctx, r.pool)

	payload, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}

	_, err = q.Exec(ctx,
		`INSERT INTO outbox_events (id, aggregate_id, event_type, payload, correlation_id, causation_id, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID, e.AggregateID, e.Type, payload,
		nullableString(e.CorrelationID), nullableString(e.CausationID), e.OccurredAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.ErrAlreadyExists
		}
		return err
	}
	return nil
}
