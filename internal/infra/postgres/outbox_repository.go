package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
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

	version := e.Version
	if version == 0 {
		version = 1
	}

	_, err = q.Exec(ctx,
		`INSERT INTO outbox_events (id, aggregate_id, event_type, payload, correlation_id, causation_id, occurred_at, version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID, e.AggregateID, e.Type, payload,
		nullableString(e.CorrelationID), nullableString(e.CausationID), e.OccurredAt, version,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (r *OutboxRepository) Claim(ctx context.Context, workerID string, limit int, lockTTL time.Duration) ([]ports.OutboxRecord, error) {
	q := querierFrom(ctx, r.pool)

	rows, err := q.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM outbox_events
			WHERE published_at IS NULL
			  AND next_attempt_at <= now()
			  AND (locked_by IS NULL OR locked_at < now() - make_interval(secs => $3))
			ORDER BY occurred_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox_events o
		SET locked_by = $1, locked_at = now()
		FROM claimed
		WHERE o.id = claimed.id
		RETURNING o.id, o.aggregate_id, o.event_type, o.payload, o.correlation_id, o.causation_id, o.occurred_at, o.version, o.attempts`,
		workerID, limit, lockTTL.Seconds(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]ports.OutboxRecord, 0)
	for rows.Next() {
		var rec ports.OutboxRecord
		var correlationID, causationID *string
		if err := rows.Scan(&rec.ID, &rec.AggregateID, &rec.EventType, &rec.Payload, &correlationID, &causationID, &rec.OccurredAt, &rec.Version, &rec.Attempts); err != nil {
			return nil, err
		}
		if correlationID != nil {
			rec.CorrelationID = *correlationID
		}
		if causationID != nil {
			rec.CausationID = *causationID
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *OutboxRepository) MarkPublished(ctx context.Context, id string) error {
	q := querierFrom(ctx, r.pool)
	tag, err := q.Exec(ctx, `UPDATE outbox_events SET published_at = now(), locked_by = NULL, locked_at = NULL WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, nextAttemptAt time.Time) error {
	q := querierFrom(ctx, r.pool)
	tag, err := q.Exec(ctx, `UPDATE outbox_events SET attempts = attempts + 1, next_attempt_at = $2, locked_by = NULL, locked_at = NULL WHERE id = $1`, id, nextAttemptAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
