package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

func loginUser(t *testing.T, m *session.Manager, userID int64) string {
	t.Helper()
	raw, err := m.Login(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func wantUnauthenticated(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}

func TestValidateValidSession(t *testing.T) {
	m, _, clk := newManager()
	raw := loginUser(t, m, 42)
	clk.Advance(5 * time.Minute)

	s, err := m.Validate(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.UserID != 42 {
		t.Errorf("UserID = %d, want 42", s.UserID)
	}
}

func TestValidateTouchesLastSeen(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	clk.Advance(10 * time.Minute)

	if _, err := m.Validate(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	want := startTime.Add(10 * time.Minute)
	if got := store.sessions[0].LastSeenAt; !got.Equal(want) {
		t.Errorf("LastSeenAt = %v, want %v", got, want)
	}
}

func TestValidateIdleTimeout(t *testing.T) {
	tests := []struct {
		name    string
		idle    time.Duration
		wantErr bool
	}{
		{"just under idle limit", 29 * time.Minute, false},
		{"just over idle limit", 31 * time.Minute, true},
		{"long idle", 5 * time.Hour, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _, clk := newManager()
			raw := loginUser(t, m, 1)
			clk.Advance(tc.idle)

			_, err := m.Validate(context.Background(), raw)
			if tc.wantErr {
				wantUnauthenticated(t, err)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateActivityExtendsIdle(t *testing.T) {
	m, _, clk := newManager()
	raw := loginUser(t, m, 1)

	clk.Advance(20 * time.Minute)
	if _, err := m.Validate(context.Background(), raw); err != nil {
		t.Fatalf("first request: %v", err)
	}
	clk.Advance(20 * time.Minute) // 40 min after login, but only 20 since last use
	if _, err := m.Validate(context.Background(), raw); err != nil {
		t.Fatalf("second request should still be valid: %v", err)
	}
}

func TestValidateAbsoluteExpiryEvenWhenActive(t *testing.T) {
	m, _, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	for elapsed := 20 * time.Minute; elapsed < 8*time.Hour; elapsed += 20 * time.Minute {
		clk.Advance(20 * time.Minute)
		if _, err := m.Validate(ctx, raw); err != nil {
			t.Fatalf("active session rejected early at %v: %v", elapsed, err)
		}
	}

	clk.Advance(20 * time.Minute) // exactly 8h after login
	_, err := m.Validate(ctx, raw)
	wantUnauthenticated(t, err)
}

func TestValidateRevokedSession(t *testing.T) {
	m, store, _ := newManager()
	raw := loginUser(t, m, 1)
	revokedAt := startTime
	store.sessions[0].RevokedAt = &revokedAt

	_, err := m.Validate(context.Background(), raw)
	wantUnauthenticated(t, err)
}

func TestValidateUnknownAndMalformedTokens(t *testing.T) {
	m, _, _ := newManager()
	loginUser(t, m, 1)

	otherRaw, _, err := session.NewToken() // well-formed, but never stored
	if err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string]string{
		"unknown token": otherRaw,
		"empty":         "",
		"garbage":       "!!!not-a-token!!!",
		"wrong length":  "QUJD",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := m.Validate(context.Background(), raw)
			wantUnauthenticated(t, err)
		})
	}
}

func TestValidateStoreFailureIsNotUnauthenticated(t *testing.T) {
	m, store, _ := newManager()
	raw := loginUser(t, m, 1)
	dbErr := errors.New("db down")
	store.getErr = dbErr

	_, err := m.Validate(context.Background(), raw)
	if !errors.Is(err, dbErr) {
		t.Fatalf("want the store error wrapped, got %v", err)
	}
	if errors.Is(err, session.ErrUnauthenticated) {
		t.Error("an outage must not look like a logged-out user")
	}
}

func TestValidateTouchFailureIsNotUnauthenticated(t *testing.T) {
	m, store, _ := newManager()
	raw := loginUser(t, m, 1)
	dbErr := errors.New("db down")
	store.touchErr = dbErr

	_, err := m.Validate(context.Background(), raw)
	if !errors.Is(err, dbErr) {
		t.Fatalf("want the store error wrapped, got %v", err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	m, _, _ := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	if err := m.Logout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	_, err := m.Validate(ctx, raw)
	wantUnauthenticated(t, err)
}

func TestLogoutOnlyAffectsThatSession(t *testing.T) {
	m, _, _ := newManager()
	first := loginUser(t, m, 1)
	second := loginUser(t, m, 1)
	ctx := context.Background()

	if err := m.Logout(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Validate(ctx, second); err != nil {
		t.Fatalf("other session of the same user must survive: %v", err)
	}
}

func TestLogoutMalformedToken(t *testing.T) {
	m, _, _ := newManager()
	wantUnauthenticated(t, m.Logout(context.Background(), "!!!"))
}
