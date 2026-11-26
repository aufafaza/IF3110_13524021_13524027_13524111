package platformdb

import (
	"context"
	"fmt"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("platformdb: parse config: %w", err)
	}
	cfg.ConnConfig.Tracer = observability.NewPGXTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("platformdb: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("platformdb: ping: %w", err)
	}
	return pool, nil
}
