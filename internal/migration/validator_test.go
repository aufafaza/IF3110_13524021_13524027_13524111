package migration_test

import (
	"errors"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func TestValidateTitle(t *testing.T) {
	if err := migration.ValidateTitle(""); !errors.Is(err, migration.ErrEmptyTitle) {
		t.Errorf("empty title: got %v, want ErrEmptyTitle", err)
	}
	if err := migration.ValidateTitle("   \t\n  "); !errors.Is(err, migration.ErrEmptyTitle) {
		t.Errorf("whitespace title: got %v, want ErrEmptyTitle", err)
	}
	if err := migration.ValidateTitle("Add users table"); err != nil {
		t.Errorf("valid title: unexpected error %v", err)
	}
}

func TestValidateSQLEmptyAndComments(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t  "},
		{"only semicolons", "; ; ;"},
		{"line comment only", "-- CREATE TABLE users (id int);\n-- another comment"},
		{"block comment only", "/* CREATE TABLE users (id int); */"},
		{"nested block comment only", "/* outer /* inner */ still outer */ ; -- trailing"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := migration.ValidateSQL(tc.sql)
			if !errors.Is(err, migration.ErrEmptySQL) {
				t.Fatalf("ValidateSQL(%q) = %v, want ErrEmptySQL", tc.sql, err)
			}
		})
	}
}

func TestValidateSQLValidStatements(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		wantStmts int
	}{
		{
			name:      "single create table",
			sql:       "CREATE TABLE items (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL);",
			wantStmts: 1,
		},
		{
			name: "multi-statement ddl and dml",
			sql: `
				-- Create table and seed initial row
				CREATE TABLE categories (id INT PRIMARY KEY, label TEXT NOT NULL);
				CREATE UNIQUE INDEX categories_label_idx ON categories (label);
				INSERT INTO categories (id, label) VALUES (1, 'General; Hardware');
			`,
			wantStmts: 3,
		},
		{
			name: "keywords inside strings and comments are ignored",
			sql: `
				-- CREATE INDEX CONCURRENTLY ignored_in_comment ON t(c);
				/* VACUUM FULL; BEGIN; COMMIT; */
				INSERT INTO audit_log (note) VALUES ('CREATE INDEX CONCURRENTLY and VACUUM and ROLLBACK');
			`,
			wantStmts: 1,
		},
		{
			name: "dollar quoted function body with BEGIN and END",
			sql: `
				CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS TRIGGER AS $$
				BEGIN
					NEW.updated_at = now();
					RETURN NEW;
				END;
				$$ LANGUAGE plpgsql;
			`,
			wantStmts: 1,
		},
		{
			name:      "case expression with END",
			sql:       "SELECT CASE WHEN 1 = 1 THEN 'ok' ELSE 'no' END;",
			wantStmts: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := migration.ValidateSQL(tc.sql); err != nil {
				t.Fatalf("ValidateSQL unexpected error: %v", err)
			}
			got := migration.SplitStatements(tc.sql)
			if len(got) != tc.wantStmts {
				t.Fatalf("SplitStatements count = %d, want %d (%v)", len(got), tc.wantStmts, got)
			}
		})
	}
}

func TestValidateSQLRejectsNonTransactional(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"create index concurrently", "CREATE INDEX CONCURRENTLY idx_users_email ON users (email);"},
		{"create unique index concurrently", "create unique index concurrently idx_users_code on users (code);"},
		{"drop index concurrently", "DROP INDEX CONCURRENTLY idx_users_email;"},
		{"reindex concurrently", "REINDEX TABLE CONCURRENTLY users;"},
		{"vacuum", "VACUUM ANALYZE users;"},
		{"cluster", "CLUSTER users USING idx_users_pkey;"},
		{"create database", "CREATE DATABASE another_db;"},
		{"drop database", "DROP DATABASE another_db;"},
		{"create tablespace", "CREATE TABLESPACE fastspace LOCATION '/ssd/data';"},
		{"drop tablespace", "DROP TABLESPACE fastspace;"},
		{"alter system", "ALTER SYSTEM SET max_connections = 200;"},
		{"manual begin", "BEGIN; CREATE TABLE t (id int); COMMIT;"},
		{"manual commit", "CREATE TABLE t (id int); COMMIT;"},
		{"manual rollback", "CREATE TABLE t (id int); ROLLBACK;"},
		{"manual start transaction", "START TRANSACTION; CREATE TABLE t (id int);"},
		{
			"non-transactional as second statement",
			"CREATE TABLE logs (id serial primary key, msg text);\nCREATE INDEX CONCURRENTLY idx_logs_msg ON logs(msg);",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := migration.ValidateSQL(tc.sql)
			if !errors.Is(err, migration.ErrNonTransactionalSQL) {
				t.Fatalf("ValidateSQL(%q) = %v, want ErrNonTransactionalSQL", tc.sql, err)
			}
		})
	}
}
