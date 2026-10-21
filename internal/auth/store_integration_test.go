//go:build integration

package auth_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func newTestStore(t *testing.T) *auth.PostgresStore {
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
	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		t.Fatal(err)
	}
	return auth.NewPostgresStore(pool)
}

func TestCreateThenFind(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id, err := store.Create(ctx, auth.NewUser{
		Username:        "alice",
		PasswordHash:    "hash-1",
		IsPlatformAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("want a non-zero id")
	}

	want := auth.StoredUser{
		ID:              id,
		Username:        "alice",
		PasswordHash:    "hash-1",
		IsPlatformAdmin: true,
		IsActive:        true,
	}

	byName, err := store.FindByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if byName != want {
		t.Errorf("FindByUsername = %+v, want %+v", byName, want)
	}

	byID, err := store.FindByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if byID != want {
		t.Errorf("FindByID = %+v, want %+v", byID, want)
	}
}

func TestCreateDefaultsToActiveNonAdmin(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id, err := store.Create(ctx, auth.NewUser{Username: "bob", PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.FindByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsActive || u.IsPlatformAdmin {
		t.Errorf("want active non-admin, got %+v", u)
	}
}

func TestCreateDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.Create(ctx, auth.NewUser{Username: "alice", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Create(ctx, auth.NewUser{Username: "alice", PasswordHash: "other"})
	if !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("want ErrUsernameTaken, got %v", err)
	}
}

func TestFindNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.FindByUsername(ctx, "ghost"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("FindByUsername: want ErrNotFound, got %v", err)
	}
	if _, err := store.FindByID(ctx, 999); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("FindByID: want ErrNotFound, got %v", err)
	}
}
