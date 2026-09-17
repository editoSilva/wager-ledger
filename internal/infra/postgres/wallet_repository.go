package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

type WalletRepository struct {
	pool *pgxpool.Pool
}

func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

func (r *WalletRepository) FindByID(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT id, player_id, currency, balance_minor_units, version, created_at, updated_at
		 FROM wallets WHERE id = $1`,
		string(id),
	)
	return scanWallet(row)
}

func (r *WalletRepository) FindByPlayerAndCurrency(ctx context.Context, playerID wallet.PlayerID, currency string) (*wallet.Wallet, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT id, player_id, currency, balance_minor_units, version, created_at, updated_at
		 FROM wallets WHERE player_id = $1 AND currency = $2`,
		string(playerID), currency,
	)
	return scanWallet(row)
}

func (r *WalletRepository) Create(ctx context.Context, w *wallet.Wallet) error {
	q := querierFrom(ctx, r.pool)
	_, err := q.Exec(ctx,
		`INSERT INTO wallets (id, player_id, currency, balance_minor_units, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(w.ID()), string(w.PlayerID()), w.Currency(), w.Balance().MinorUnits(), w.Version(), w.CreatedAt(), w.UpdatedAt(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (r *WalletRepository) Save(ctx context.Context, w *wallet.Wallet, previousVersion int64) error {
	q := querierFrom(ctx, r.pool)
	tag, err := q.Exec(ctx,
		`UPDATE wallets SET balance_minor_units = $1, version = $2, updated_at = $3
		 WHERE id = $4 AND version = $5`,
		w.Balance().MinorUnits(), w.Version(), w.UpdatedAt(), string(w.ID()), previousVersion,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrOptimisticLock
	}
	return nil
}

func scanWallet(row pgx.Row) (*wallet.Wallet, error) {
	var (
		id, playerID, currency     string
		balanceMinorUnits, version int64
		createdAt, updatedAt       time.Time
	)
	if err := row.Scan(&id, &playerID, &currency, &balanceMinorUnits, &version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}

	balance, err := money.FromMinorUnits(balanceMinorUnits, currency)
	if err != nil {
		return nil, err
	}

	return wallet.Rehydrate(wallet.ID(id), wallet.PlayerID(playerID), balance, version, createdAt, updatedAt)
}
