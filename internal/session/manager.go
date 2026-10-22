package session

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrUnauthenticated = errors.New("session: unauthenticated user")

type Clock interface {
	Now() time.Time
}
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

type Config struct {
	IdleTimeout, AbsoluteTimeout, RotationInterval, GracePeriod time.Duration
}

type Manager struct {
	store Store
	clock Clock
	cfg   Config
}

func NewManager(store Store, clock Clock, cfg Config) *Manager {
	return &Manager{
		store: store,
		clock: clock,
		cfg:   cfg,
	}
}

func (m *Manager) Login(ctx context.Context, userID int64) (rawToken string, err error) {
	rawToken, hash, err := NewToken()
	if err != nil {
		return "", fmt.Errorf("session: login: %w", err)
	}
	csrf, err := NewCSRFSecret()
	if err != nil {
		return "", fmt.Errorf("session: login: %w", err)
	}

	now := m.clock.Now()
	err = m.store.Create(ctx, Session{
		IDHash:            hash,
		UserID:            userID,
		CSRFSecret:        csrf,
		CreatedAt:         now,
		LastSeenAt:        now,
		RotatedAt:         now,
		AbsoluteExpiresAt: now.Add(m.cfg.AbsoluteTimeout),
	})
	if err != nil {
		return "", fmt.Errorf("session: login: %w", err)
	}
	return rawToken, nil
}
