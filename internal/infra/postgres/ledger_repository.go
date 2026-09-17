package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

type LedgerRepository struct {
	pool *pgxpool.Pool
}

func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

func (r *LedgerRepository) Create(ctx context.Context, e *ledger.Entry) error {
	q := querierFrom(ctx, r.pool)
	_, err := q.Exec(ctx,
		`INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor_units, currency,
			balance_before_minor_units, balance_after_minor_units, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		string(e.ID()), string(e.WalletID()), string(e.TransactionID()), string(e.Direction()),
		e.Amount().MinorUnits(), e.Amount().Currency(),
		e.BalanceBefore().MinorUnits(), e.BalanceAfter().MinorUnits(),
		e.CreatedAt(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (r *LedgerRepository) SumByWallet(ctx context.Context, walletID wallet.ID) (money.Money, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount_minor_units ELSE -amount_minor_units END), 0),
			COALESCE(MAX(currency), '')
		 FROM wallet_ledger_entries WHERE wallet_id = $1`,
		string(walletID),
	)

	var sumMinorUnits int64
	var currency string
	if err := row.Scan(&sumMinorUnits, &currency); err != nil {
		return money.Money{}, err
	}
	if currency == "" {
		return money.Money{}, ports.ErrNotFound
	}

	return money.FromMinorUnits(sumMinorUnits, currency)
}
