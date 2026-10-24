package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func TestDatabaseDSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return dsn
}

func OpenPlatformTestPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := TestDatabaseDSN(t)
	ctx := context.Background()

	pool, err := platformdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("testutil: open platform db: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("testutil: reset public schema: %v", err)
	}
	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		t.Fatalf("testutil: apply platform migrations: %v", err)
	}
	return pool
}

func OpenIsolatedSchemaPool(t testing.TB, schema string) *pgxpool.Pool {
	t.Helper()
	dsn := TestDatabaseDSN(t)
	ctx := context.Background()

	ident := pgx.Identifier{schema}.Sanitize()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("testutil: parse dsn: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = ident

	adminPool, err := platformdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("testutil: open admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE; CREATE SCHEMA "+ident); err != nil {
		adminPool.Close()
		t.Fatalf("testutil: recreate schema %s: %v", schema, err)
	}
	adminPool.Close()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testutil: open schema pool %s: %v", schema, err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanPool, err := platformdb.Open(context.Background(), dsn)
		if err == nil {
			_, _ = cleanPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+ident+" CASCADE")
			cleanPool.Close()
		}
	})
	return pool
}

type StaticProvider struct {
	Pools map[int64]*pgxpool.Pool
}

func (p *StaticProvider) Pool(_ context.Context, databaseID int64) (*pgxpool.Pool, error) {
	if p == nil || p.Pools == nil {
		return nil, dbmanager.ErrDatabaseNotFound
	}
	pool, ok := p.Pools[databaseID]
	if !ok || pool == nil {
		return nil, dbmanager.ErrDatabaseNotFound
	}
	return pool, nil
}

func SeedUser(t testing.TB, pool *pgxpool.Pool, username string, isAdmin, isActive bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, is_platform_admin, is_active)
		 VALUES ($1, 'test-hash', $2, $3)
		 RETURNING id`,
		username, isAdmin, isActive,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil: seed user %s: %v", username, err)
	}
	return id
}

func SeedGroup(t testing.TB, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO groups (name) VALUES ($1) RETURNING id`,
		name,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil: seed group %s: %v", name, err)
	}
	return id
}

func SeedUserGroup(t testing.TB, pool *pgxpool.Pool, userID, groupID int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)`,
		userID, groupID,
	)
	if err != nil {
		t.Fatalf("testutil: seed user_groups (%d, %d): %v", userID, groupID, err)
	}
}

func SeedManagedDatabase(t testing.TB, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO managed_databases (name, host, port, db_name, db_user, password_enc, sslmode)
		 VALUES ($1, 'localhost', 5432, 'target_db', 'test', $2, 'disable')
		 RETURNING id`,
		name, []byte("encrypted-secret"),
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil: seed managed_database %s: %v", name, err)
	}
	return id
}

func SeedGroupGrant(t testing.TB, pool *pgxpool.Pool, groupID, databaseID int64, level string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)`,
		groupID, databaseID, level,
	)
	if err != nil {
		t.Fatalf("testutil: seed group_db_access (%d, %d, %s): %v", groupID, databaseID, level, err)
	}
}
