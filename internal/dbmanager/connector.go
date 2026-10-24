package dbmanager

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const connectTimeout = 5 * time.Second

type ConnError struct{ Reason string }

func (e *ConnError) Error() string        { return "dbmanager: connection failed: " + e.Reason }
func (e *ConnError) Is(target error) bool { return target == ErrConnectionFailed }

func mapConnError(err error) error {
	var pgErr *pgconn.PgError
	var netErr net.Error
	switch {
	case errors.As(err, &pgErr) && (pgErr.Code == "28P01" || pgErr.Code == "28000"):
		return &ConnError{"username atau password database salah"}
	case errors.As(err, &pgErr) && pgErr.Code == "3D000":
		return &ConnError{"database tidak ditemukan di server"}
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return &ConnError{"koneksi timeout"}
	default:
		return &ConnError{"server tidak dapat dihubungi"}
	}
}

func (in DatabaseInput) dsn() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(in.DBUser, in.Password),
		Host:     net.JoinHostPort(in.Host, strconv.Itoa(in.Port)),
		Path:     "/" + in.DBName,
		RawQuery: url.Values{"sslmode": {in.SSLMode}}.Encode(),
	}
	return u.String()
}

func connect(ctx context.Context, in DatabaseInput) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(in.dsn())
	if err != nil {
		return nil, &ConnError{"parameter koneksi tidak valid"}
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, mapConnError(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, mapConnError(err)
	}
	return pool, nil
}

func TestConnection(ctx context.Context, in DatabaseInput) error {
	if err := in.validate(true); err != nil {
		return err
	}
	pool, err := connect(ctx, in)
	if err != nil {
		return err
	}
	pool.Close()
	return nil
}