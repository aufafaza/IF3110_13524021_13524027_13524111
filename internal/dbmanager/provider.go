package dbmanager

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDatabaseNotFound = errors.New("dbmanager: database not found")
	ErrConnectionFailed = errors.New("dbmanager: connection failed")
)

type Provider interface {
	Pool(ctx context.Context, databaseID int64) (*pgxpool.Pool, error)
}
