package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReadinessChecker struct {
	pool *pgxpool.Pool
}

func NewReadinessChecker(pool *pgxpool.Pool) *ReadinessChecker {
	return &ReadinessChecker{pool: pool}
}

func (c *ReadinessChecker) Name() string {
	return "postgres"
}

func (c *ReadinessChecker) Ready(ctx context.Context) error {
	return c.pool.Ping(ctx)
}
