package ports

import (
	"context"
	"errors"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

var ErrNotFound = errors.New("ports: registro não encontrado")
var ErrOptimisticLock = errors.New("ports: conflito de versão otimista")
var ErrAlreadyExists = errors.New("ports: registro já existe")

type WalletRepository interface {
	FindByID(ctx context.Context, id wallet.ID) (*wallet.Wallet, error)
	FindByPlayerAndCurrency(ctx context.Context, playerID wallet.PlayerID, currency string) (*wallet.Wallet, error)
	Create(ctx context.Context, w *wallet.Wallet) error
	Save(ctx context.Context, w *wallet.Wallet, previousVersion int64) error
}

type WagerTransactionRepository interface {
	FindByID(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error)
	FindByIDForUpdate(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error)
	FindByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagertx.WagerTransaction, error)
	FindByIdempotencyKey(ctx context.Context, idempotencyKey string) (*wagertx.WagerTransaction, error)
	FindProcessedReversalByReference(ctx context.Context, referenceID wagertx.ID) (*wagertx.WagerTransaction, error)
	ListPendingReferenceIDs(ctx context.Context, limit int) ([]wagertx.ID, error)
	ListStalePendingReferenceIDs(ctx context.Context, olderThan time.Time, limit int) ([]wagertx.ID, error)
	Create(ctx context.Context, tx *wagertx.WagerTransaction) error
	Update(ctx context.Context, tx *wagertx.WagerTransaction) error
}

type LedgerRepository interface {
	Create(ctx context.Context, e *ledger.Entry) error
	SumByWallet(ctx context.Context, walletID wallet.ID) (money.Money, error)
	CountByWallet(ctx context.Context, walletID wallet.ID) (int, error)
	ListByWallet(ctx context.Context, walletID wallet.ID, cursor string, limit int) ([]*ledger.Entry, string, error)
}

type OutboxRecord struct {
	ID            string
	AggregateID   string
	EventType     string
	Payload       []byte
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Version       int
	Attempts      int
}

type OutboxRepository interface {
	Create(ctx context.Context, e event.Event) error
	Claim(ctx context.Context, workerID string, limit int, lockTTL time.Duration) ([]OutboxRecord, error)
	MarkPublished(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string, nextAttemptAt time.Time) error
}

type InboxRepository interface {
	Create(ctx context.Context, consumerName, messageID, messageHash string) error
	MarkCompleted(ctx context.Context, consumerName, messageID string) error
}

// SnapshotReader executa fn dentro de uma transação somente-leitura com
// snapshot consistente (REPEATABLE READ), garantindo que múltiplas leituras
// (ex.: saldo da carteira + soma do ledger) vejam o mesmo estado do banco.
// Sem isso, uma escrita concorrente entre as leituras pode fazer a
// reconciliação reportar divergência onde não há corrupção real de dados.
type SnapshotReader interface {
	ReadSnapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

type UnitOfWork interface {
	Execute(ctx context.Context, fn func(ctx context.Context) error) error
}

type IDGenerator interface {
	NewID() string
}
