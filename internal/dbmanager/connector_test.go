package dbmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMapConnError(t *testing.T) {
	const authFail = "username atau password database salah"
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{"password salah", &pgconn.PgError{Code: "28P01"}, authFail},
		{"role tidak ada", &pgconn.PgError{Code: "28000"}, authFail},
		{"error terbungkus", fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "28P01"}), authFail},
		{"database tidak ada", &pgconn.PgError{Code: "3D000"}, "database tidak ditemukan di server"},
		{"timeout", context.DeadlineExceeded, "koneksi timeout"},
		{"lainnya", errors.New("dial tcp: connection refused"), "server tidak dapat dihubungi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapConnError(tt.err)
			var ce *ConnError
			if !errors.As(got, &ce) || ce.Reason != tt.reason {
				t.Fatalf("got %v, want reason %q", got, tt.reason)
			}
			if !errors.Is(got, ErrConnectionFailed) {
				t.Error("tidak cocok dengan ErrConnectionFailed")
			}
		})
	}
}

func TestDSNEscapesSpecialCharacters(t *testing.T) {
	in := DatabaseInput{Host: "db.local", Port: 6543, DBName: "my db", DBUser: "u@x",
		Password: "p@ss:w/rd?#%", SSLMode: "require"}
	cfg, err := pgxpool.ParseConfig(in.dsn())
	if err != nil {
		t.Fatal(err)
	}
	cc := cfg.ConnConfig
	if cc.Host != "db.local" || cc.Port != 6543 || cc.Database != "my db" || cc.User != "u@x" || cc.Password != in.Password {
		t.Fatalf("hasil parse tidak cocok: host=%q port=%d db=%q user=%q", cc.Host, cc.Port, cc.Database, cc.User)
	}
}