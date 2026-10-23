package session_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

func TestRotation_NotDueBeforeInterval(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	clk.Advance(15*time.Minute - time.Second)

	got, err := m.Validate(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.NewToken != "" {
		t.Errorf("rotated early: NewToken = %q", got.NewToken)
	}
	if store.rotateCalls != 0 {
		t.Errorf("Rotate called %d times, want 0", store.rotateCalls)
	}
}

func TestRotation_DueAtInterval(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	oldHash := append([]byte(nil), store.sessions[0].IDHash...)
	clk.Advance(15 * time.Minute)

	got, err := m.Validate(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.NewToken == "" {
		t.Fatal("no new token at the rotation interval")
	}
	if got.NewToken == raw {
		t.Error("new token equals the old one")
	}

	s := store.sessions[0]
	newHash, err := session.HashToken(got.NewToken)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.IDHash, newHash) {
		t.Error("stored IDHash is not the hash of the returned NewToken")
	}
	if !bytes.Equal(s.PrevIDHash, oldHash) {
		t.Error("previous ID hash was not kept for the grace period")
	}
	if want := clk.now.Add(30 * time.Second); s.PrevValidUntil == nil || !s.PrevValidUntil.Equal(want) {
		t.Errorf("PrevValidUntil = %v, want %v", s.PrevValidUntil, want)
	}
	if !s.RotatedAt.Equal(clk.now) {
		t.Errorf("RotatedAt = %v, want %v", s.RotatedAt, clk.now)
	}
}

func TestRotation_NewTokenWorks(t *testing.T) {
	m, _, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	rotated, err := m.Validate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(time.Second)
	if _, err := m.Validate(ctx, rotated.NewToken); err != nil {
		t.Fatalf("new token rejected: %v", err)
	}
}

func TestRotation_GracePeriod(t *testing.T) {
	tests := []struct {
		name    string
		after   time.Duration
		wantErr bool
	}{
		{"immediately after rotation", 0, false},
		{"10s after rotation", 10 * time.Second, false},
		{"just inside grace", 30*time.Second - time.Millisecond, false},
		{"exactly at grace end", 30 * time.Second, true},
		{"45s after rotation", 45 * time.Second, true},
		{"minutes later", 5 * time.Minute, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _, clk := newManager()
			raw := loginUser(t, m, 1)
			ctx := context.Background()

			clk.Advance(15 * time.Minute)
			if _, err := m.Validate(ctx, raw); err != nil {
				t.Fatal(err)
			}

			clk.Advance(tc.after)
			_, err := m.Validate(ctx, raw)
			if tc.wantErr {
				wantUnauthenticated(t, err)
			} else if err != nil {
				t.Fatalf("old token rejected inside grace: %v", err)
			}
		})
	}
}

func TestRotation_PreviousIDDoesNotRotateAgain(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	first, err := m.Validate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if first.NewToken == "" {
		t.Fatal("first request should have rotated")
	}
	callsAfterFirst := store.rotateCalls
	idAfterFirst := append([]byte(nil), store.sessions[0].IDHash...)

	clk.Advance(5 * time.Second)
	second, err := m.Validate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if second.NewToken != "" {
		t.Errorf("previous ID was handed a new token: %q", second.NewToken)
	}
	if store.rotateCalls != callsAfterFirst {
		t.Error("Rotate called again for a previous-ID request")
	}
	if !bytes.Equal(store.sessions[0].IDHash, idAfterFirst) {
		t.Error("current ID changed on a previous-ID request")
	}
}

func TestRotation_PreviousIDInGraceTouchesSession(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	if _, err := m.Validate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	if _, err := m.Validate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if want := clk.now; !store.sessions[0].LastSeenAt.Equal(want) {
		t.Errorf("LastSeenAt = %v, want %v", store.sessions[0].LastSeenAt, want)
	}
}

func TestRotation_DoesNotExtendAbsoluteExpiry(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()
	want := store.sessions[0].AbsoluteExpiresAt

	for i := 0; i < 5; i++ {
		clk.Advance(15 * time.Minute)
		got, err := m.Validate(ctx, raw)
		if err != nil {
			t.Fatal(err)
		}
		raw = followRotation(raw, got)
	}
	if store.rotateCalls != 5 {
		t.Fatalf("Rotate called %d times, want 5", store.rotateCalls)
	}
	if got := store.sessions[0].AbsoluteExpiresAt; !got.Equal(want) {
		t.Errorf("AbsoluteExpiresAt moved from %v to %v", want, got)
	}
}

func TestRotation_PreviousIDCannotOutliveRevocation(t *testing.T) {
	m, _, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	rotated, err := m.Validate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Logout(ctx, rotated.NewToken); err != nil {
		t.Fatal(err)
	}

	clk.Advance(5 * time.Second)
	_, err = m.Validate(ctx, raw)
	wantUnauthenticated(t, err)
}

func TestRotation_PreviousIDCannotOutliveAbsoluteExpiry(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	if _, err := m.Validate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	store.sessions[0].AbsoluteExpiresAt = clk.now.Add(time.Second)

	clk.Advance(2 * time.Second)
	_, err := m.Validate(ctx, raw)
	wantUnauthenticated(t, err)
}

func TestRotation_LostRaceStillServesRequest(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	store.rotateErr = session.ErrNotFound
	clk.Advance(15 * time.Minute)

	got, err := m.Validate(context.Background(), raw)
	if err != nil {
		t.Fatalf("lost race must not fail the request: %v", err)
	}
	if got.NewToken != "" {
		t.Errorf("NewToken = %q after a lost race, want empty", got.NewToken)
	}
}

func TestRotation_StoreFailureIsNotUnauthenticatedAndIssuesNoToken(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	dbErr := errors.New("db down")
	store.rotateErr = dbErr
	clk.Advance(15 * time.Minute)

	got, err := m.Validate(context.Background(), raw)
	if !errors.Is(err, dbErr) {
		t.Fatalf("want the store error wrapped, got %v", err)
	}
	if errors.Is(err, session.ErrUnauthenticated) {
		t.Error("an outage must not look like a logged-out user")
	}
	if got.NewToken != "" {
		t.Error("a token was issued although rotation failed")
	}
}

func TestRotation_DueAgainAfterAnotherInterval(t *testing.T) {
	m, store, clk := newManager()
	raw := loginUser(t, m, 1)
	ctx := context.Background()

	clk.Advance(15 * time.Minute)
	first, err := m.Validate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(14 * time.Minute)
	early, err := m.Validate(ctx, first.NewToken)
	if err != nil {
		t.Fatal(err)
	}
	if early.NewToken != "" {
		t.Error("rotated again before a full interval passed")
	}

	clk.Advance(time.Minute)
	second, err := m.Validate(ctx, first.NewToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.NewToken == "" {
		t.Fatal("no second rotation after another full interval")
	}
	if store.rotateCalls != 2 {
		t.Errorf("Rotate called %d times, want 2", store.rotateCalls)
	}
}
