package migration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
)

const migrationLockNamespace int32 = 727002

type Locker interface {
	Acquire(ctx context.Context, databaseID int64) (release func() error, err error)
}

type TargetPostgresLocker struct {
	provider dbmanager.Provider
}

func NewTargetPostgresLocker(provider dbmanager.Provider) *TargetPostgresLocker {
	return &TargetPostgresLocker{provider: provider}
}

func (l *TargetPostgresLocker) Acquire(ctx context.Context, databaseID int64) (func() error, error) {
	pool, err := l.provider.Pool(ctx, databaseID)
	if err != nil {
		return nil, fmt.Errorf("migration: lock pool: %w", err)
	}
	return acquireAdvisoryLock(ctx, pool, databaseID)
}

type PoolPostgresLocker struct {
	pool *pgxpool.Pool
}

func NewPoolPostgresLocker(pool *pgxpool.Pool) *PoolPostgresLocker {
	return &PoolPostgresLocker{pool: pool}
}

func (l *PoolPostgresLocker) Acquire(ctx context.Context, databaseID int64) (func() error, error) {
	return acquireAdvisoryLock(ctx, l.pool, databaseID)
}

func acquireAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, databaseID int64) (func() error, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration: acquire lock connection: %w", err)
	}

	lockKey := int32(databaseID)
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", migrationLockNamespace, lockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("migration: acquire advisory lock: %w", err)
	}

	var once sync.Once
	var unlockErr error
	release := func() error {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, unlockErr = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", migrationLockNamespace, lockKey)
			conn.Release()
		})
		return unlockErr
	}
	return release, nil
}
