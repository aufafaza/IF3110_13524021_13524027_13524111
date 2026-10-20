//go:build integration

package platformdb_test

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := platformdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestMigrateAppliesPlatformSchema(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"users", "groups", "user_groups", "sessions",
		"managed_databases", "group_db_access", "migrations",
	} {
		var exists bool
		err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err=%v)", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	for i := 0; i < 2; i++ {
		if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("schema_migrations rows = %d, want 5", n)
	}
}

func TestMigrateFailureRollsBackThatFile(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	fsys := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE ok_table (id int);")},
		"0002_bad.sql": {Data: []byte("CREATE TABLE half_table (id int); SELECT broken_syntax FROM;")},
	}
	if err := platformdb.Migrate(ctx, pool, fsys); err == nil {
		t.Fatal("want error from bad migration")
	}

	var okExists, halfExists bool
	pool.QueryRow(ctx, "SELECT to_regclass('public.ok_table') IS NOT NULL").Scan(&okExists)
	pool.QueryRow(ctx, "SELECT to_regclass('public.half_table') IS NOT NULL").Scan(&halfExists)
	if !okExists {
		t.Error("first migration should stay applied")
	}
	if halfExists {
		t.Error("failed migration must be rolled back completely")
	}
}
