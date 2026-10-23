package session

import (
	"bytes"
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

func (m *Manager) Validate(ctx context.Context, rawToken string) (Session, error) {
	hash, err := HashToken(rawToken)
	if err != nil {
		return Session{}, ErrUnauthenticated
	}

	sess, err := m.store.GetByIDHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Session{}, ErrUnauthenticated
	}

	if err != nil {
		return Session{}, fmt.Errorf("session: validate: %w", err)
	}

	hashID := sess.IDHash
	if !bytes.Equal(hash, hashID) {
		return Session{}, ErrUnauthenticated
	}

	if sess.RevokedAt != nil {
		return Session{}, ErrUnauthenticated
	}

	if !m.clock.Now().Before(sess.AbsoluteExpiresAt) {
		return Session{}, ErrUnauthenticated
	}

	if m.clock.Now().Sub(sess.LastSeenAt) > m.cfg.IdleTimeout {
		return Session{}, ErrUnauthenticated
	}

	if err := m.store.Touch(ctx, hashID, m.clock.Now()); err != nil {
		return Session{}, fmt.Errorf("session: validate: %w", err)
	}
	return sess, nil
}

func (m *Manager) Logout(ctx context.Context, rawToken string) error {
	hash, err := HashToken(rawToken)
	if err != nil {
		return ErrUnauthenticated
	}

	// else then just revoke
	err = m.store.Revoke(ctx, hash, m.clock.Now())
	if err != nil {
		return fmt.Errorf("session: logout: %w", err)
	}

	return nil
}

func (m *Manager) RevokeAllForUser(ctx context.Context, userID int64) error {
	err := m.store.RevokeAllForUser(ctx, userID, m.clock.Now())
	if err != nil {
		return fmt.Errorf("session: revoke all for user: %w", err)
	}

	return nil
}
