package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type UserStore interface {
	FindByUsername(ctx context.Context, username string) (StoredUser, error)
	FindByID(ctx context.Context, ID int64) (StoredUser, error)
	Create(ctx context.Context, u NewUser) (int64, error)
}

type Service struct {
	store     UserStore
	hasher    PasswordHasher
	dummyHash string
}

var ErrInvalidCredentials = errors.New("auth: invalid credentials")

func NewService(store UserStore, hasher PasswordHasher) (*Service, error) {
	dummy, err := hasher.Hash("dummy-password-for-timing")
	if err != nil {
		return nil, fmt.Errorf("auth: init service %w", err)
	}
	return &Service{store: store, hasher: hasher, dummyHash: dummy}, nil
}

func (s Service) Login(ctx context.Context, username, password string) (access.User, error) {
	u, err := s.store.FindByUsername(ctx, username)
	if errors.Is(err, ErrNotFound) {
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return access.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return access.User{}, fmt.Errorf("auth login:  %w", err)
	}
	ok, err := s.hasher.Verify(password, u.PasswordHash)
	if err != nil {
		return access.User{}, fmt.Errorf("auth login: %w", err)
	}

	if !ok || !u.IsActive {
		return access.User{}, ErrInvalidCredentials
	}

	return access.User{
		ID:              u.ID,
		Username:        u.Username,
		IsPlatformAdmin: u.IsPlatformAdmin,
	}, nil
}

/*
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}
func (h Argon2idHasher) Verify(password, encodedHash string) (bool, error) {
	return VerifyPassword(password, encodedHash)
}

*/
