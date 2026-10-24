//go:build integration

package session_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

type env struct {
	store *session.PostgresStore
	users *auth.PostgresStore
}

func newEnv(t *testing.T) env {
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
	return env{store: session.NewPostgresStore(pool), users: auth.NewPostgresStore(pool)}
}

func (e env) newUser(t *testing.T, name string) int64 {
	t.Helper()
	id, err := e.users.Create(context.Background(), auth.CreateUserParams{Username: name, PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Postgres stores microseconds, so truncate to keep round-trip comparisons exact.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func newSession(userID int64, hash string) session.Session {
	n := now()
	return session.Session{
		IDHash:            []byte(hash),
		UserID:            userID,
		CSRFSecret:        []byte("csrf-secret"),
		CreatedAt:         n,
		LastSeenAt:        n,
		RotatedAt:         n,
		AbsoluteExpiresAt: n.Add(8 * time.Hour),
	}
}

func TestCreateThenGet(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	uid := e.newUser(t, "alice")
	want := newSession(uid, "hash-1")

	if err := e.store.Create(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetByIDHash(ctx, want.IDHash)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got.IDHash, want.IDHash) || got.UserID != uid ||
		!bytes.Equal(got.CSRFSecret, want.CSRFSecret) {
		t.Errorf("identity fields differ: %+v", got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.LastSeenAt.Equal(want.LastSeenAt) ||
		!got.RotatedAt.Equal(want.RotatedAt) || !got.AbsoluteExpiresAt.Equal(want.AbsoluteExpiresAt) {
		t.Errorf("time fields differ: %+v", got)
	}
	if got.PrevIDHash != nil || got.PrevValidUntil != nil || got.RevokedAt != nil {
		t.Errorf("want nil prev/revoked fields, got %+v", got)
	}
}

func TestGetUnknown(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.GetByIDHash(context.Background(), []byte("nope")); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRotate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	uid := e.newUser(t, "alice")
	orig := newSession(uid, "old")
	if err := e.store.Create(ctx, orig); err != nil {
		t.Fatal(err)
	}

	rotatedAt := now().Add(15 * time.Minute)
	graceUntil := rotatedAt.Add(30 * time.Second)
	if err := e.store.Rotate(ctx, []byte("old"), []byte("new"), rotatedAt, graceUntil); err != nil {
		t.Fatal(err)
	}

	byNew, err := e.store.GetByIDHash(ctx, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if string(byNew.IDHash) != "new" || string(byNew.PrevIDHash) != "old" {
		t.Errorf("id/prev wrong: id=%q prev=%q", byNew.IDHash, byNew.PrevIDHash)
	}
	if byNew.PrevValidUntil == nil || !byNew.PrevValidUntil.Equal(graceUntil) {
		t.Errorf("PrevValidUntil = %v, want %v", byNew.PrevValidUntil, graceUntil)
	}
	if !byNew.RotatedAt.Equal(rotatedAt) {
		t.Errorf("RotatedAt = %v, want %v", byNew.RotatedAt, rotatedAt)
	}
	if !byNew.AbsoluteExpiresAt.Equal(orig.AbsoluteExpiresAt) {
		t.Errorf("rotation changed AbsoluteExpiresAt: %v -> %v", orig.AbsoluteExpiresAt, byNew.AbsoluteExpiresAt)
	}

	byOld, err := e.store.GetByIDHash(ctx, []byte("old"))
	if err != nil {
		t.Fatalf("old id should still resolve during grace: %v", err)
	}
	if string(byOld.IDHash) != "new" {
		t.Errorf("old id resolved to a different session: %q", byOld.IDHash)
	}
}

func TestRotateTwiceKeepsOnlyLatestPrevious(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.store.Create(ctx, newSession(e.newUser(t, "alice"), "h1")); err != nil {
		t.Fatal(err)
	}
	grace := now().Add(time.Minute)
	if err := e.store.Rotate(ctx, []byte("h1"), []byte("h2"), now(), grace); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Rotate(ctx, []byte("h2"), []byte("h3"), now(), grace); err != nil {
		t.Fatal(err)
	}

	if _, err := e.store.GetByIDHash(ctx, []byte("h1")); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("h1 should be gone after two rotations, got %v", err)
	}
	if _, err := e.store.GetByIDHash(ctx, []byte("h2")); err != nil {
		t.Errorf("h2 should still resolve as previous: %v", err)
	}
}

func TestRotateUnknown(t *testing.T) {
	e := newEnv(t)
	err := e.store.Rotate(context.Background(), []byte("ghost"), []byte("new"), now(), now())
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRotateRejectsOldHashAsSource(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.store.Create(ctx, newSession(e.newUser(t, "alice"), "old")); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Rotate(ctx, []byte("old"), []byte("new"), now(), now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	err := e.store.Rotate(ctx, []byte("old"), []byte("other"), now(), now().Add(time.Minute))
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("rotating from a previous id must fail with ErrNotFound, got %v", err)
	}
}

func TestTouch(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.store.Create(ctx, newSession(e.newUser(t, "alice"), "h1")); err != nil {
		t.Fatal(err)
	}

	later := now().Add(10 * time.Minute)
	if err := e.store.Touch(ctx, []byte("h1"), later); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetByIDHash(ctx, []byte("h1"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("LastSeenAt = %v, want %v", got.LastSeenAt, later)
	}
}

func TestTouchUnknown(t *testing.T) {
	e := newEnv(t)
	if err := e.store.Touch(context.Background(), []byte("ghost"), now()); err == nil {
		t.Fatal("want an error when no session matches")
	}
}

func TestRevoke(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.store.Create(ctx, newSession(e.newUser(t, "alice"), "h1")); err != nil {
		t.Fatal(err)
	}

	at := now().Add(time.Minute)
	if err := e.store.Revoke(ctx, []byte("h1"), at); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetByIDHash(ctx, []byte("h1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil || !got.RevokedAt.Equal(at) {
		t.Errorf("RevokedAt = %v, want %v", got.RevokedAt, at)
	}
}

func TestRevokeAllForUser(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.newUser(t, "alice")
	bob := e.newUser(t, "bob")
	for _, s := range []session.Session{
		newSession(alice, "a1"), newSession(alice, "a2"), newSession(bob, "b1"),
	} {
		if err := e.store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	first := now().Add(time.Minute)
	if err := e.store.Revoke(ctx, []byte("a1"), first); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Hour)
	if err := e.store.RevokeAllForUser(ctx, alice, second); err != nil {
		t.Fatal(err)
	}

	a1, _ := e.store.GetByIDHash(ctx, []byte("a1"))
	a2, _ := e.store.GetByIDHash(ctx, []byte("a2"))
	b1, _ := e.store.GetByIDHash(ctx, []byte("b1"))

	if a2.RevokedAt == nil || !a2.RevokedAt.Equal(second) {
		t.Errorf("a2 should be revoked at %v, got %v", second, a2.RevokedAt)
	}
	if a1.RevokedAt == nil || !a1.RevokedAt.Equal(first) {
		t.Errorf("a1 keeps its original revocation time %v, got %v", first, a1.RevokedAt)
	}
	if b1.RevokedAt != nil {
		t.Errorf("another user's session must not be revoked, got %v", b1.RevokedAt)
	}
}

func TestRevokeAllForUserWithNoSessions(t *testing.T) {
	e := newEnv(t)
	uid := e.newUser(t, "alice")
	if err := e.store.RevokeAllForUser(context.Background(), uid, now()); err != nil {
		t.Fatalf("no sessions is not an error, got %v", err)
	}
}
