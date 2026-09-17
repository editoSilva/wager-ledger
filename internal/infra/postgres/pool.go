package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/config"
)

func NewPool(lc fx.Lifecycle, cfg config.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: erro ao criar pool de conexões: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			pool.Close()
			return nil
		},
	})

	return pool, nil
}
