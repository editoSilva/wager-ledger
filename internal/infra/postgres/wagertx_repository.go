package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

const wagerTxColumns = `
	id, kind, status, wallet_id, player_id, amount_minor_units, currency,
	provider_id, external_transaction_id, idempotency_key, payload_hash,
	round_id, game_id, reference_external_transaction_id,
	resolved_reference_id, failure_code,
	financial_result_minor_units, financial_result_currency,
	created_at, updated_at
`

type WagerTransactionRepository struct {
	pool *pgxpool.Pool
}

func NewWagerTransactionRepository(pool *pgxpool.Pool) *WagerTransactionRepository {
	return &WagerTransactionRepository{pool: pool}
}

func (r *WagerTransactionRepository) FindByID(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx, `SELECT `+wagerTxColumns+` FROM wager_transactions WHERE id = $1`, string(id))
	return scanWagerTx(row)
}

func (r *WagerTransactionRepository) FindByIDForUpdate(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx, `SELECT `+wagerTxColumns+` FROM wager_transactions WHERE id = $1 FOR UPDATE`, string(id))
	return scanWagerTx(row)
}

func (r *WagerTransactionRepository) FindByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagertx.WagerTransaction, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT `+wagerTxColumns+` FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2`,
		providerID, externalID,
	)
	return scanWagerTx(row)
}

func (r *WagerTransactionRepository) FindByIdempotencyKey(ctx context.Context, idempotencyKey string) (*wagertx.WagerTransaction, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT `+wagerTxColumns+` FROM wager_transactions WHERE idempotency_key = $1`,
		idempotencyKey,
	)
	return scanWagerTx(row)
}

func (r *WagerTransactionRepository) FindProcessedReversalByReference(ctx context.Context, referenceID wagertx.ID) (*wagertx.WagerTransaction, error) {
	q := querierFrom(ctx, r.pool)
	row := q.QueryRow(ctx,
		`SELECT `+wagerTxColumns+` FROM wager_transactions
		 WHERE resolved_reference_id = $1 AND kind IN ('REFUND', 'ROLLBACK') AND status = 'PROCESSED'`,
		string(referenceID),
	)
	return scanWagerTx(row)
}

func (r *WagerTransactionRepository) ListPendingReferenceIDs(ctx context.Context, limit int) ([]wagertx.ID, error) {
	q := querierFrom(ctx, r.pool)
	rows, err := q.Query(ctx, `SELECT id FROM wager_transactions WHERE status = 'PENDING_REFERENCE' ORDER BY created_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]wagertx.ID, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, wagertx.ID(id))
	}
	return ids, rows.Err()
}

func (r *WagerTransactionRepository) Create(ctx context.Context, tx *wagertx.WagerTransaction) error {
	q := querierFrom(ctx, r.pool)

	var financialResultMinorUnits, financialResultCurrency any
	if fr := tx.FinancialResult(); fr != nil {
		financialResultMinorUnits = fr.MinorUnits()
		financialResultCurrency = fr.Currency()
	}

	_, err := q.Exec(ctx,
		`INSERT INTO wager_transactions (`+wagerTxColumns+`) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
		)`,
		string(tx.ID()), string(tx.Kind()), string(tx.Status()), string(tx.WalletID()), string(tx.PlayerID()),
		tx.Money().MinorUnits(), tx.Money().Currency(),
		nullableString(tx.ProviderID()), nullableString(tx.ExternalID()), nullableString(tx.IdempotencyKey()), nullableString(tx.PayloadHash()),
		nullableString(tx.RoundID()), nullableString(tx.GameID()), nullableString(tx.ReferenceExternalID()),
		nullableString(string(tx.ResolvedReferenceID())), nullableString(tx.FailureCode()),
		financialResultMinorUnits, financialResultCurrency,
		tx.CreatedAt(), tx.UpdatedAt(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (r *WagerTransactionRepository) Update(ctx context.Context, tx *wagertx.WagerTransaction) error {
	q := querierFrom(ctx, r.pool)

	var financialResultMinorUnits, financialResultCurrency any
	if fr := tx.FinancialResult(); fr != nil {
		financialResultMinorUnits = fr.MinorUnits()
		financialResultCurrency = fr.Currency()
	}

	tag, err := q.Exec(ctx,
		`UPDATE wager_transactions SET
			status = $1, resolved_reference_id = $2, failure_code = $3,
			financial_result_minor_units = $4, financial_result_currency = $5,
			updated_at = $6
		 WHERE id = $7`,
		string(tx.Status()), nullableString(string(tx.ResolvedReferenceID())), nullableString(tx.FailureCode()),
		financialResultMinorUnits, financialResultCurrency, tx.UpdatedAt(), string(tx.ID()),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func scanWagerTx(row pgx.Row) (*wagertx.WagerTransaction, error) {
	var (
		id, kind, status, walletID, playerID, currency                                            string
		amountMinorUnits                                                                          int64
		providerID, externalID, idempotencyKey, payloadHash, roundID, gameID, referenceExternalID *string
		resolvedReferenceID, failureCode                                                          *string
		financialResultMinorUnits                                                                 *int64
		financialResultCurrency                                                                   *string
		createdAt, updatedAt                                                                      time.Time
	)

	err := row.Scan(
		&id, &kind, &status, &walletID, &playerID, &amountMinorUnits, &currency,
		&providerID, &externalID, &idempotencyKey, &payloadHash, &roundID, &gameID, &referenceExternalID,
		&resolvedReferenceID, &failureCode, &financialResultMinorUnits, &financialResultCurrency,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}

	amount, err := money.FromMinorUnits(amountMinorUnits, currency)
	if err != nil {
		return nil, err
	}

	var financialResult *money.Money
	if financialResultMinorUnits != nil && financialResultCurrency != nil {
		fr, err := money.FromMinorUnits(*financialResultMinorUnits, *financialResultCurrency)
		if err != nil {
			return nil, err
		}
		financialResult = &fr
	}

	tx, err := wagertx.Rehydrate(
		wagertx.ID(id), wagertx.Kind(kind), wagertx.Status(status),
		wallet.ID(walletID), wallet.PlayerID(playerID), amount,
		deref(providerID), deref(externalID), deref(idempotencyKey), deref(payloadHash),
		deref(roundID), deref(gameID), deref(referenceExternalID),
		wagertx.ID(deref(resolvedReferenceID)), deref(failureCode), financialResult,
		createdAt, updatedAt,
	)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
