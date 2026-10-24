package dbmanager

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrTableNotFound = errors.New("dbmanager: table not found")

type (
	Table struct{ Schema, Name string }
	Column struct {
		Name, Type string
		Nullable   bool
		Default    *string
	}
	Constraint struct{ Name, Type, Definition string }
	Index      struct {
		Name      string
		Unique    bool
		Method    string
		Definition string
	}
	TableInfo struct {
		Columns     []Column
		Constraints []Constraint
		Indexes     []Index
	}
)

type Viewer struct{ provider Provider }

func NewViewer(p Provider) *Viewer { return &Viewer{p} }

const userTables = `FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')`

func (v *Viewer) readTx(ctx context.Context, dbID int64, fn func(pgx.Tx) error) error {
	pool, err := v.provider.Pool(ctx, dbID)
	if err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapConnError(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = 10000"); err != nil {
		return err
	}
	return fn(tx)
}

func collect[T any](ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]T, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

func requireTable(ctx context.Context, tx pgx.Tx, schema, name string) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 `+userTables+` AND n.nspname = $1 AND c.relname = $2)`,
		schema, name).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTableNotFound
	}
	return nil
}

func (v *Viewer) Tables(ctx context.Context, dbID int64) (out []Table, err error) {
	err = v.readTx(ctx, dbID, func(tx pgx.Tx) (err error) {
		out, err = collect[Table](ctx, tx,
			`SELECT n.nspname::text, c.relname::text `+userTables+` ORDER BY 1, 2`)
		return
	})
	return out, err
}

func (v *Viewer) TableInfo(ctx context.Context, dbID int64, schema, table string) (info TableInfo, err error) {
	err = v.readTx(ctx, dbID, func(tx pgx.Tx) (err error) {
		if err = requireTable(ctx, tx, schema, table); err != nil {
			return
		}
		if info.Columns, err = collect[Column](ctx, tx,
			`SELECT a.attname::text, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
			        pg_get_expr(d.adbin, d.adrelid)
			 FROM pg_attribute a
			 JOIN pg_class c ON c.oid = a.attrelid
			 JOIN pg_namespace n ON n.oid = c.relnamespace
			 LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
			 WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
			 ORDER BY a.attnum`, schema, table); err != nil {
			return
		}
		if info.Constraints, err = collect[Constraint](ctx, tx,
			`SELECT con.conname::text,
			        CASE con.contype WHEN 'p' THEN 'PRIMARY KEY' WHEN 'f' THEN 'FOREIGN KEY'
			             WHEN 'u' THEN 'UNIQUE' WHEN 'c' THEN 'CHECK' ELSE 'EXCLUDE' END,
			        pg_get_constraintdef(con.oid)
			 FROM pg_constraint con
			 JOIN pg_class c ON c.oid = con.conrelid
			 JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p','f','u','c','x')
			 ORDER BY con.contype, con.conname`, schema, table); err != nil {
			return
		}
		info.Indexes, err = collect[Index](ctx, tx,
			`SELECT ic.relname::text, i.indisunique, am.amname::text, pg_get_indexdef(i.indexrelid)
			 FROM pg_index i
			 JOIN pg_class ic ON ic.oid = i.indexrelid
			 JOIN pg_class c ON c.oid = i.indrelid
			 JOIN pg_namespace n ON n.oid = c.relnamespace
			 JOIN pg_am am ON am.oid = ic.relam
			 WHERE n.nspname = $1 AND c.relname = $2
			 ORDER BY ic.relname`, schema, table)
		return
	})
	return info, err
}