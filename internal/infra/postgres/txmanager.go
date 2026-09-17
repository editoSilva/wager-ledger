package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txCtxKey struct{}

func WithinTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}

	txCtx := context.WithValue(ctx, txCtxKey{}, tx)

	if err := fn(txCtx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

func querierFrom(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

type PgUnitOfWork struct {
	pool *pgxpool.Pool
}

func NewUnitOfWork(pool *pgxpool.Pool) *PgUnitOfWork {
	return &PgUnitOfWork{pool: pool}
}

func (u *PgUnitOfWork) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	return WithinTx(ctx, u.pool, fn)
}
