package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Session struct {
	IDHash            []byte
	PrevIDHash        []byte
	PrevValidUntil    *time.Time
	UserID            int64
	CSRFSecret        []byte
	CreatedAt         time.Time
	LastSeenAt        time.Time
	RotatedAt         time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
}

var ErrNotFound = errors.New("session: not found")

type Store interface {
	Create(ctx context.Context, s Session) error
	GetByIDHash(ctx context.Context, hash []byte) (Session, error)
	Touch(ctx context.Context, idHash []byte, now time.Time) error
	Rotate(ctx context.Context, oldHash, newHash []byte, now, graceUntil time.Time) error
	Revoke(ctx context.Context, idHash []byte, now time.Time) error
	RevokeAllForUser(ctx context.Context, userID int64, now time.Time) error
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Create(ctx context.Context, sess Session) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions
			(id_hash, user_id, csrf_secret, created_at, last_seen_at, rotated_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sess.IDHash, sess.UserID, sess.CSRFSecret,
		sess.CreatedAt, sess.LastSeenAt, sess.RotatedAt, sess.AbsoluteExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("session: create: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetByIDHash(ctx context.Context, hash []byte) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx,
		`SELECT id_hash, prev_id_hash, prev_valid_until, user_id, csrf_secret,
			created_at, last_seen_at, rotated_at, absolute_expires_at, revoked_at
		FROM sessions
		WHERE id_hash = $1 OR prev_id_hash = $1`,
		hash,
	).Scan(
		&sess.IDHash, &sess.PrevIDHash, &sess.PrevValidUntil, &sess.UserID, &sess.CSRFSecret,
		&sess.CreatedAt, &sess.LastSeenAt, &sess.RotatedAt, &sess.AbsoluteExpiresAt, &sess.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session: get by id hash: %w", err)
	}
	return sess, nil
}


func (s*PostgresStore) Touch(ctx context.Context, idHash []byte, now time.Time) error { 
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions 
			SET last_seen_at = $2 WHERE id_hash = $1	
		`, 
		idHash, now)	

	if err != nil {
		return fmt.Errorf("session: touch: %w", err)
	}
	if (tag.RowsAffected() == 0) { 
		return fmt.Errorf("session: touch: row affected = 0)"); 
	} 

	return nil

} 

func (s *PostgresStore) Rotate(ctx context.Context, oldHash, newHash []byte, now, graceUntil time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions
		SET prev_id_hash = id_hash,
			prev_valid_until = $4,
			id_hash = $2,
			rotated_at = $3
		WHERE id_hash = $1`,
		oldHash, newHash, now, graceUntil,
	)
	if err != nil {
		return fmt.Errorf("session: rotate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) Revoke(ctx context.Context, idHash []byte, now time.Time) error{ 
	_, err := s.pool.Exec(ctx, 
		` 
			UPDATE sessions
			SET revoked_at = $2 WHERE id_hash = $1
		`, idHash, now) 

	if (err != nil){ 
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil 
} 

func (s *PostgresStore) RevokeAllForUser(ctx context.Context, userID int64, now time.Time) error { 
	_, err := s.pool.Exec(ctx, 
		` 
			UPDATE sessions
			SET revoked_at = $2 WHERE user_id = $1 AND revoked_at is NULL 
		`, userID, now) 

	if (err != nil){ 
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil 
} 


