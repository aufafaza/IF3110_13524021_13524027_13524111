package session_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type fakeStore struct {
	sessions  []session.Session
	createErr error
	getErr    error
	touchErr  error
	rotateErr error

	rotateCalls int
}

func (f *fakeStore) Create(ctx context.Context, s session.Session) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.sessions = append(f.sessions, s)
	return nil
}

func (f *fakeStore) find(hash []byte) int {
	for i, s := range f.sessions {
		if bytes.Equal(s.IDHash, hash) || (s.PrevIDHash != nil && bytes.Equal(s.PrevIDHash, hash)) {
			return i
		}
	}
	return -1
}

func (f *fakeStore) GetByIDHash(ctx context.Context, hash []byte) (session.Session, error) {
	if f.getErr != nil {
		return session.Session{}, f.getErr
	}
	if i := f.find(hash); i >= 0 {
		return f.sessions[i], nil
	}
	return session.Session{}, session.ErrNotFound
}

func (f *fakeStore) Touch(ctx context.Context, idHash []byte, now time.Time) error {
	if f.touchErr != nil {
		return f.touchErr
	}
	i := f.find(idHash)
	if i < 0 {
		return session.ErrNotFound
	}
	f.sessions[i].LastSeenAt = now
	return nil
}

func (f *fakeStore) Revoke(ctx context.Context, idHash []byte, now time.Time) error {
	if i := f.find(idHash); i >= 0 {
		f.sessions[i].RevokedAt = &now
	}
	return nil
}

func (f *fakeStore) RevokeAllForUser(ctx context.Context, userID int64, now time.Time) error {
	for i := range f.sessions {
		if f.sessions[i].UserID == userID && f.sessions[i].RevokedAt == nil {
			t := now
			f.sessions[i].RevokedAt = &t
		}
	}
	return nil
}

func (f *fakeStore) Rotate(ctx context.Context, oldHash, newHash []byte, now, graceUntil time.Time) error {
	f.rotateCalls++
	if f.rotateErr != nil {
		return f.rotateErr
	}
	for i := range f.sessions {
		if bytes.Equal(f.sessions[i].IDHash, oldHash) {
			until := graceUntil
			f.sessions[i].PrevIDHash = oldHash
			f.sessions[i].PrevValidUntil = &until
			f.sessions[i].IDHash = newHash
			f.sessions[i].RotatedAt = now
			return nil
		}
	}
	return session.ErrNotFound
}

var testConfig = session.Config{
	IdleTimeout:      30 * time.Minute,
	AbsoluteTimeout:  8 * time.Hour,
	RotationInterval: 15 * time.Minute,
	GracePeriod:      30 * time.Second,
}

var startTime = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func newManager() (*session.Manager, *fakeStore, *fakeClock) {
	store := &fakeStore{}
	clk := &fakeClock{now: startTime}
	return session.NewManager(store, clk, testConfig), store, clk
}

func TestLoginStoresSession(t *testing.T) {
	m, store, _ := newManager()

	raw, err := m.Login(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("raw token is empty")
	}
	if len(store.sessions) != 1 {
		t.Fatalf("stored %d sessions, want 1", len(store.sessions))
	}

	s := store.sessions[0]
	if s.UserID != 42 {
		t.Errorf("UserID = %d, want 42", s.UserID)
	}
	if len(s.CSRFSecret) != 32 {
		t.Errorf("CSRFSecret length = %d, want 32", len(s.CSRFSecret))
	}
	if s.PrevIDHash != nil || s.PrevValidUntil != nil || s.RevokedAt != nil {
		t.Errorf("new session must have no prev/revoked state: %+v", s)
	}
}

func TestLoginSetsTimes(t *testing.T) {
	m, store, _ := newManager()

	if _, err := m.Login(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	s := store.sessions[0]

	for name, got := range map[string]time.Time{
		"CreatedAt":  s.CreatedAt,
		"LastSeenAt": s.LastSeenAt,
		"RotatedAt":  s.RotatedAt,
	} {
		if !got.Equal(startTime) {
			t.Errorf("%s = %v, want %v", name, got, startTime)
		}
	}
	if want := startTime.Add(8 * time.Hour); !s.AbsoluteExpiresAt.Equal(want) {
		t.Errorf("AbsoluteExpiresAt = %v, want %v", s.AbsoluteExpiresAt, want)
	}
}

func TestLoginUsesClock(t *testing.T) {
	m, store, clk := newManager()
	clk.Advance(3 * time.Hour)

	if _, err := m.Login(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	s := store.sessions[0]

	if want := startTime.Add(3 * time.Hour); !s.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", s.CreatedAt, want)
	}
	if want := startTime.Add(11 * time.Hour); !s.AbsoluteExpiresAt.Equal(want) {
		t.Errorf("AbsoluteExpiresAt = %v, want %v", s.AbsoluteExpiresAt, want)
	}
}

func TestLoginTokenMatchesStoredHash(t *testing.T) {
	m, store, _ := newManager()

	raw, err := m.Login(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := session.HashToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, store.sessions[0].IDHash) {
		t.Fatal("HashToken(raw) does not equal the stored IDHash")
	}
	if bytes.Equal(store.sessions[0].IDHash, []byte(raw)) {
		t.Fatal("the raw token must never be what is stored")
	}
}

func TestLoginIssuesDistinctSessions(t *testing.T) {
	m, store, _ := newManager()

	raw1, _ := m.Login(context.Background(), 1)
	raw2, _ := m.Login(context.Background(), 1)

	if raw1 == raw2 {
		t.Error("two logins returned the same token")
	}
	if bytes.Equal(store.sessions[0].IDHash, store.sessions[1].IDHash) {
		t.Error("two logins stored the same hash")
	}
	if bytes.Equal(store.sessions[0].CSRFSecret, store.sessions[1].CSRFSecret) {
		t.Error("two logins share a CSRF secret")
	}
}

func TestLoginStoreFailure(t *testing.T) {
	m, store, _ := newManager()
	dbErr := errors.New("db down")
	store.createErr = dbErr

	raw, err := m.Login(context.Background(), 1)
	if !errors.Is(err, dbErr) {
		t.Fatalf("want the store error wrapped, got %v", err)
	}
	if errors.Is(err, session.ErrUnauthenticated) {
		t.Error("a store failure must not look like an authentication failure")
	}
	if raw != "" {
		t.Errorf("no token should be returned on failure, got %q", raw)
	}
}
