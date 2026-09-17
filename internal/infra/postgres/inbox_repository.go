package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
)

type InboxRepository struct{ pool *pgxpool.Pool }

func NewInboxRepository(pool *pgxpool.Pool) *InboxRepository { return &InboxRepository{pool: pool} }

func (r *InboxRepository) Create(ctx context.Context, consumerName, messageID, messageHash string) error {
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `INSERT INTO inbox_messages (consumer_name, message_id, message_hash) VALUES ($1,$2,$3)`, consumerName, messageID, messageHash)
	if isUniqueViolation(err) {
		return ports.ErrAlreadyExists
	}
	return err
}

func (r *InboxRepository) MarkCompleted(ctx context.Context, consumerName, messageID string) error {
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `UPDATE inbox_messages SET completed_at = now() WHERE consumer_name = $1 AND message_id = $2`, consumerName, messageID)
	return err
}
