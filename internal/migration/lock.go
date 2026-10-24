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

type localGate struct {
	mu    sync.Mutex
	gates map[int64]chan struct{}
}

func (g *localGate) lock(ctx context.Context, databaseID int64) (func(), error) {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[int64]chan struct{})
	}
	ch, ok := g.gates[databaseID]
	if !ok {
		ch = make(chan struct{}, 1)
		g.gates[databaseID] = ch
	}
	g.mu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("migration: acquire advisory lock: %w", ctx.Err())
	}
}

type TargetPostgresLocker struct {
	provider dbmanager.Provider
	gate     localGate
}

func NewTargetPostgresLocker(provider dbmanager.Provider) *TargetPostgresLocker {
	return &TargetPostgresLocker{provider: provider}
}

func (l *TargetPostgresLocker) Acquire(ctx context.Context, databaseID int64) (func() error, error) {
	unlockLocal, err := l.gate.lock(ctx, databaseID)
	if err != nil {
		return nil, err
	}

	pool, err := l.provider.Pool(ctx, databaseID)
	if err != nil {
		unlockLocal()
		return nil, fmt.Errorf("migration: lock pool: %w", err)
	}
	return acquireAdvisoryLock(ctx, pool, databaseID, unlockLocal)
}

type PoolPostgresLocker struct {
	pool *pgxpool.Pool
	gate localGate
}

func NewPoolPostgresLocker(pool *pgxpool.Pool) *PoolPostgresLocker {
	return &PoolPostgresLocker{pool: pool}
}

func (l *PoolPostgresLocker) Acquire(ctx context.Context, databaseID int64) (func() error, error) {
	unlockLocal, err := l.gate.lock(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	return acquireAdvisoryLock(ctx, l.pool, databaseID, unlockLocal)
}

func acquireAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, databaseID int64, unlockLocal func()) (func() error, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		unlockLocal()
		return nil, fmt.Errorf("migration: acquire lock connection: %w", err)
	}

	lockKey := int32(databaseID)
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", migrationLockNamespace, lockKey); err != nil {
		conn.Release()
		unlockLocal()
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
			unlockLocal()
		})
		return unlockErr
	}
	return release, nil
}
