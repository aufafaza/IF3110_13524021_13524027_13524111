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

type Validated struct {
	Session  Session
	NewToken string
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

func (m *Manager) Validate(ctx context.Context, rawToken string) (Validated, error) {
	hash, err := HashToken(rawToken)
	if err != nil {
		return Validated{}, ErrUnauthenticated
	}

	sess, err := m.store.GetByIDHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Validated{}, ErrUnauthenticated
	}
	if err != nil {
		return Validated{}, fmt.Errorf("session: validate: %w", err)
	}

	now := m.clock.Now()

	isCurrent := bytes.Equal(hash, sess.IDHash)
	inGrace := !isCurrent && sess.PrevValidUntil != nil && now.Before(*sess.PrevValidUntil)
	if !isCurrent && !inGrace {
		return Validated{}, ErrUnauthenticated
	}

	if sess.RevokedAt != nil {
		return Validated{}, ErrUnauthenticated
	}

	if !now.Before(sess.AbsoluteExpiresAt) {
		return Validated{}, ErrUnauthenticated
	}

	if now.Sub(sess.LastSeenAt) > m.cfg.IdleTimeout {
		return Validated{}, ErrUnauthenticated
	}

	if err := m.store.Touch(ctx, sess.IDHash, now); err != nil {
		return Validated{}, fmt.Errorf("session: validate: %w", err)
	}

	if !isCurrent || now.Sub(sess.RotatedAt) < m.cfg.RotationInterval {
		return Validated{Session: sess}, nil
	}

	newRaw, newHash, err := NewToken()
	if err != nil {
		return Validated{}, fmt.Errorf("session: validate: %w", err)
	}
	err = m.store.Rotate(ctx, sess.IDHash, newHash, now, now.Add(m.cfg.GracePeriod))
	if errors.Is(err, ErrNotFound) {
		return Validated{Session: sess}, nil
	}
	if err != nil {
		return Validated{}, fmt.Errorf("session: validate: rotate: %w", err)
	}
	return Validated{Session: sess, NewToken: newRaw}, nil
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
