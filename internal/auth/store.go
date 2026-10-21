package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StoredUser struct {
	ID              int64
	Username        string
	PasswordHash    string
	IsPlatformAdmin bool
	IsActive        bool
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

type NewUser struct { 
	Username				string 
	PasswordHash		string
	IsPlatformAdmin	bool
} 

var (
	ErrNotFound      = errors.New("user not found")
	ErrUsernameTaken = errors.New("username is taken")
)

func (s *PostgresStore) FindByUsername(ctx context.Context, username string) (user StoredUser, err error) {
	var u StoredUser
	err = s.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, is_platform_admin, is_active 
				FROM users
			WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredUser{}, ErrNotFound
	}

	if err != nil {
		return StoredUser{}, fmt.Errorf("auth: find by username %w", err)
	}
	return u, nil
}

func (s *PostgresStore) FindByID(ctx context.Context, ID int64) (user StoredUser, err error) {
	var u StoredUser
	err = s.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, is_platform_admin, is_active 
			FROM users 
			WHERE id = $1`,
		ID,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredUser{}, ErrNotFound
	}

	if err != nil {
		return StoredUser{}, fmt.Errorf("auth: find by ID %w", err)
	}
	return u, nil

}

func (s *PostgresStore) Create(ctx context.Context, u NewUser) (int64, error) {

	var id int64 
	err := s.pool.QueryRow(ctx, 
		`INSERT INTO users(username, password_hash, is_platform_admin) 
		VALUES($1, $2, $3) 
		RETURNING id`,
		u.Username, u.PasswordHash, u.IsPlatformAdmin,
		).Scan(&id)
	var pgErr *pgconn.PgError 
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { 
		return 0, ErrUsernameTaken
	}
	if err != nil { 
		return 0, fmt.Errorf("auth: create user: %w", err) 
	}

	return id, nil
}
