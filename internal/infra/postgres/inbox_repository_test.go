package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/editosilva/wager-ledger/internal/application/ports"
)

// TestInboxRepository_Create_DuplicateWithinSameTransaction_DoesNotAbortTransaction
// reproduz a regressão em que a violação de unicidade capturada dentro de uma
// transação deixava a transação em estado abortado no Postgres, fazendo o
// commit subsequente falhar mesmo quando o chamador tratava a duplicidade
// como um no-op esperado (ports.ErrAlreadyExists).
func TestInboxRepository_Create_DuplicateWithinSameTransaction_DoesNotAbortTransaction(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewInboxRepository(pool)

	consumerName := "test-consumer"
	messageID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM inbox_messages WHERE consumer_name = $1 AND message_id = $2`, consumerName, messageID)
	})

	err := WithinTx(ctx, pool, func(txCtx context.Context) error {
		if err := repo.Create(txCtx, consumerName, messageID, "hash-1"); err != nil {
			return err
		}
		if err := repo.Create(txCtx, consumerName, messageID, "hash-1"); !errors.Is(err, ports.ErrAlreadyExists) {
			t.Fatalf("segundo Create() = %v, esperado ports.ErrAlreadyExists", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx: commit não deveria falhar após duplicidade tratada como no-op: %v", err)
	}

	var count int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM inbox_messages WHERE consumer_name = $1 AND message_id = $2`, consumerName, messageID)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("erro ao contar inbox_messages: %v", err)
	}
	if count != 1 {
		t.Errorf("inbox_messages = %d, esperado 1", count)
	}
}
