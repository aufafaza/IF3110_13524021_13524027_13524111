package dbmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidInput = errors.New("dbmanager: invalid input")
	ErrNameTaken    = errors.New("dbmanager: database name already used")
)

var validSSLModes = map[string]bool{
	"disable": true, "allow": true, "prefer": true,
	"require": true, "verify-ca": true, "verify-full": true,
}

type Database struct {
	ID        int64
	Name      string
	Host      string
	Port      int
	DBName    string
	DBUser    string
	SSLMode   string
	CreatedAt time.Time
}

type DatabaseInput struct {
	Name, Host, DBName, DBUser, Password, SSLMode string
	Port                                          int
}

func (in *DatabaseInput) validate(requirePassword bool) error {
	if in.Port == 0 {
		in.Port = 5432
	}
	if in.SSLMode == "" {
		in.SSLMode = "prefer"
	}
	switch {
	case in.Name == "" || in.Host == "" || in.DBName == "" || in.DBUser == "":
		return fmt.Errorf("%w: name, host, db name, dan user wajib diisi", ErrInvalidInput)
	case in.Port < 1 || in.Port > 65535:
		return fmt.Errorf("%w: port harus 1-65535", ErrInvalidInput)
	case requirePassword && in.Password == "":
		return fmt.Errorf("%w: password wajib diisi", ErrInvalidInput)
	case !validSSLModes[in.SSLMode]:
		return fmt.Errorf("%w: sslmode tidak dikenal", ErrInvalidInput)
	}
	return nil
}

type Registry struct {
	platform *pgxpool.Pool
	cipher   *Cipher
	mu       sync.Mutex
	pools    map[int64]*pgxpool.Pool
}

var _ Provider = (*Registry)(nil)

func NewRegistry(platform *pgxpool.Pool, c *Cipher) *Registry {
	return &Registry{platform: platform, cipher: c, pools: map[int64]*pgxpool.Pool{}}
}

const dbCols = `id, name, host, port, db_name, db_user, sslmode, created_at`

func scanDatabase(row pgx.Row) (Database, error) {
	var d Database
	err := row.Scan(&d.ID, &d.Name, &d.Host, &d.Port, &d.DBName, &d.DBUser, &d.SSLMode, &d.CreatedAt)
	return d, err
}

func mapWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrNameTaken
	}
	return err
}

func (r *Registry) Create(ctx context.Context, in DatabaseInput) (Database, error) {
	if err := in.validate(true); err != nil {
		return Database{}, err
	}
	enc, err := r.cipher.Encrypt([]byte(in.Password))
	if err != nil {
		return Database{}, err
	}
	d, err := scanDatabase(r.platform.QueryRow(ctx,
		`INSERT INTO managed_databases (name, host, port, db_name, db_user, password_enc, sslmode)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+dbCols,
		in.Name, in.Host, in.Port, in.DBName, in.DBUser, enc, in.SSLMode))
	return d, mapWriteErr(err)
}

func (r *Registry) List(ctx context.Context) ([]Database, error) {
	rows, err := r.platform.Query(ctx, `SELECT `+dbCols+` FROM managed_databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Database, error) { return scanDatabase(row) })
}

func (r *Registry) Get(ctx context.Context, id int64) (Database, error) {
	d, err := scanDatabase(r.platform.QueryRow(ctx, `SELECT `+dbCols+` FROM managed_databases WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrDatabaseNotFound
	}
	return d, err
}

func (r *Registry) Update(ctx context.Context, id int64, in DatabaseInput) error {
	if err := in.validate(false); err != nil {
		return err
	}
	var enc []byte
	if in.Password != "" {
		var err error
		if enc, err = r.cipher.Encrypt([]byte(in.Password)); err != nil {
			return err
		}
	}
	tag, err := r.platform.Exec(ctx,
		`UPDATE managed_databases SET name=$2, host=$3, port=$4, db_name=$5, db_user=$6,
		        sslmode=$7, password_enc=COALESCE($8, password_enc) WHERE id=$1`,
		id, in.Name, in.Host, in.Port, in.DBName, in.DBUser, in.SSLMode, enc)
	if err != nil {
		return mapWriteErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDatabaseNotFound
	}
	r.evict(id)
	return nil
}

func (r *Registry) Delete(ctx context.Context, id int64) error {
	tag, err := r.platform.Exec(ctx, `DELETE FROM managed_databases WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDatabaseNotFound
	}
	r.evict(id)
	return nil
}

func (r *Registry) Pool(ctx context.Context, id int64) (*pgxpool.Pool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.pools[id]; ok {
		return p, nil
	}
	in, err := r.loadParams(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := connect(ctx, in)
	if err != nil {
		return nil, err
	}
	r.pools[id] = p
	return p, nil
}

func (r *Registry) loadParams(ctx context.Context, id int64) (DatabaseInput, error) {
	var in DatabaseInput
	var enc []byte
	err := r.platform.QueryRow(ctx,
		`SELECT host, port, db_name, db_user, password_enc, sslmode FROM managed_databases WHERE id=$1`, id).
		Scan(&in.Host, &in.Port, &in.DBName, &in.DBUser, &enc, &in.SSLMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrDatabaseNotFound
	}
	if err != nil {
		return in, err
	}
	pw, err := r.cipher.Decrypt(enc)
	if err != nil {
		return in, fmt.Errorf("dbmanager: decrypt password: %w", err)
	}
	in.Password = string(pw)
	return in, nil
}

func (r *Registry) evict(id int64) {
	r.mu.Lock()
	p, ok := r.pools[id]
	delete(r.pools, id)
	r.mu.Unlock()
	if ok {
		go p.Close()
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.pools {
		p.Close()
		delete(r.pools, id)
	}
}