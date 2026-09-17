package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
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

func (r *LedgerRepository) CountByWallet(ctx context.Context, walletID wallet.ID) (int, error) {
	q := querierFrom(ctx, r.pool)
	var count int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, string(walletID)).Scan(&count)
	return count, err
}

const defaultLedgerPageSize = 50

func (r *LedgerRepository) ListByWallet(ctx context.Context, walletID wallet.ID, cursor string, limit int) ([]*ledger.Entry, string, error) {
	if limit <= 0 {
		limit = defaultLedgerPageSize
	}
	q := querierFrom(ctx, r.pool)

	const selectCols = `id, transaction_id, direction, amount_minor_units, currency,
		balance_before_minor_units, balance_after_minor_units, created_at`

	var rows pgx.Rows
	var err error

	if cursor == "" {
		rows, err = q.Query(ctx,
			`SELECT `+selectCols+` FROM wallet_ledger_entries
			 WHERE wallet_id = $1
			 ORDER BY created_at, id
			 LIMIT $2`,
			string(walletID), limit+1,
		)
	} else {
		afterCreatedAt, afterID, decodeErr := decodeLedgerCursor(cursor)
		if decodeErr != nil {
			return nil, "", decodeErr
		}
		rows, err = q.Query(ctx,
			`SELECT `+selectCols+` FROM wallet_ledger_entries
			 WHERE wallet_id = $1 AND (created_at, id) > ($2, $3)
			 ORDER BY created_at, id
			 LIMIT $4`,
			string(walletID), afterCreatedAt, afterID, limit+1,
		)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	entries := make([]*ledger.Entry, 0, limit)
	for rows.Next() {
		var id, txID, direction, currency string
		var amountMinor, beforeMinor, afterMinor int64
		var createdAt time.Time
		if err := rows.Scan(&id, &txID, &direction, &amountMinor, &currency, &beforeMinor, &afterMinor, &createdAt); err != nil {
			return nil, "", err
		}
		amount, err := money.FromMinorUnits(amountMinor, currency)
		if err != nil {
			return nil, "", err
		}
		before, err := money.FromMinorUnits(beforeMinor, currency)
		if err != nil {
			return nil, "", err
		}
		after, err := money.FromMinorUnits(afterMinor, currency)
		if err != nil {
			return nil, "", err
		}
		entry, err := ledger.NewEntry(ledger.ID(id), walletID, wagertx.ID(txID), ledger.Direction(direction), amount, before, after, createdAt)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(entries) > limit {
		last := entries[limit-1]
		nextCursor = encodeLedgerCursor(last.CreatedAt(), string(last.ID()))
		entries = entries[:limit]
	}
	return entries, nextCursor, nil
}

func encodeLedgerCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeLedgerCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cursor inválido: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("cursor inválido")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cursor inválido: %w", err)
	}
	return createdAt, parts[1], nil
}
