package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

type userFixture struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	PlatformAdmin bool   `json:"platform_admin"`
}

type groupFixture struct {
	Name    string   `json:"name"`
	Leader  string   `json:"leader_username"`
	Members []string `json:"members"`
}

type databaseFixture struct {
	Name     string            `json:"name"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	DBName   string            `json:"db_name"`
	DBUser   string            `json:"db_user"`
	Password string            `json:"password"`
	SSLMode  string            `json:"sslmode"`
	Grants   map[string]string `json:"grants"`
}

type customerFixture struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
}

type migrationFixture struct {
	Title   string `json:"title"`
	SQL     string `json:"sql"`
	DownSQL string `json:"down_sql"`
}

var databaseNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func main() {
	fixtureDir := flag.String("fixture-dir", "seed/demo", "directory containing the JSON seed fixtures")
	platformURL := flag.String("platform-url", getenv("DATABASE_URL", "postgres://platform:platform@database:5432/platform?sslmode=disable"), "platform PostgreSQL connection string")
	flag.Parse()

	if err := run(context.Background(), *fixtureDir, *platformURL); err != nil {
		fmt.Fprintln(os.Stderr, "seed-demo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, fixtureDir, platformURL string) error {
	users, err := readJSON[[]userFixture](fixtureDir, "users.json")
	if err != nil {
		return err
	}
	groups, err := readJSON[[]groupFixture](fixtureDir, "groups.json")
	if err != nil {
		return err
	}
	dbFixture, err := readJSON[databaseFixture](fixtureDir, "database.json")
	if err != nil {
		return err
	}
	customers, err := readJSON[[]customerFixture](fixtureDir, "target_customers.json")
	if err != nil {
		return err
	}
	migration, err := readJSON[migrationFixture](fixtureDir, "migration.json")
	if err != nil {
		return err
	}

	key, err := encryptionKey()
	if err != nil {
		return err
	}
	cipher, err := database.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create database cipher: %w", err)
	}

	platformPool, err := platformdb.Open(ctx, platformURL)
	if err != nil {
		return err
	}
	defer platformPool.Close()
	if err := platformdb.Migrate(ctx, platformPool, migrations.Platform()); err != nil {
		return err
	}

	if err := ensureDatabase(ctx, platformURL, dbFixture.DBName); err != nil {
		return err
	}
	targetURL, err := databaseURL(platformURL, dbFixture.DBName)
	if err != nil {
		return err
	}
	targetPool, err := platformdb.Open(ctx, targetURL)
	if err != nil {
		return fmt.Errorf("open target database: %w", err)
	}
	defer targetPool.Close()
	if err := seedTarget(ctx, targetPool, customers); err != nil {
		return err
	}

	userIDs := make(map[string]int64, len(users))
	hasher := auth.NewArgon2idHasher()
	for _, fixture := range users {
		hash, err := hasher.Hash(fixture.Password)
		if err != nil {
			return fmt.Errorf("hash password for %s: %w", fixture.Username, err)
		}
		var id int64
		err = platformPool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, is_platform_admin, is_active)
			VALUES ($1, $2, $3, true)
			ON CONFLICT (username) DO UPDATE SET
				password_hash = EXCLUDED.password_hash,
				is_platform_admin = EXCLUDED.is_platform_admin,
				is_active = true
			RETURNING id`, fixture.Username, hash, fixture.PlatformAdmin).Scan(&id)
		if err != nil {
			return fmt.Errorf("seed user %s: %w", fixture.Username, err)
		}
		userIDs[fixture.Username] = id
	}

	groupIDs := make(map[string]int64, len(groups))
	for _, fixture := range groups {
		leaderID, ok := userIDs[fixture.Leader]
		if !ok {
			return fmt.Errorf("group %s refers to unknown leader %s", fixture.Name, fixture.Leader)
		}
		var id int64
		err = platformPool.QueryRow(ctx, `
			INSERT INTO groups (name, leader_user_id)
			VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET leader_user_id = EXCLUDED.leader_user_id
			RETURNING id`, fixture.Name, leaderID).Scan(&id)
		if err != nil {
			return fmt.Errorf("seed group %s: %w", fixture.Name, err)
		}
		groupIDs[fixture.Name] = id
		for _, member := range fixture.Members {
			memberID, ok := userIDs[member]
			if !ok {
				return fmt.Errorf("group %s refers to unknown member %s", fixture.Name, member)
			}
			if _, err := platformPool.Exec(ctx, `
				INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)
				ON CONFLICT DO NOTHING`, memberID, id); err != nil {
				return fmt.Errorf("seed membership %s/%s: %w", fixture.Name, member, err)
			}
		}
	}

	encryptedPassword, err := cipher.Encrypt([]byte(dbFixture.Password))
	if err != nil {
		return fmt.Errorf("encrypt managed database password: %w", err)
	}
	var databaseID int64
	err = platformPool.QueryRow(ctx, `
		INSERT INTO managed_databases (name, host, port, db_name, db_user, password_enc, sslmode)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (name) DO UPDATE SET
			host = EXCLUDED.host,
			port = EXCLUDED.port,
			db_name = EXCLUDED.db_name,
			db_user = EXCLUDED.db_user,
			password_enc = EXCLUDED.password_enc,
			sslmode = EXCLUDED.sslmode
		RETURNING id`, dbFixture.Name, dbFixture.Host, dbFixture.Port, dbFixture.DBName,
		dbFixture.DBUser, encryptedPassword, dbFixture.SSLMode).Scan(&databaseID)
	if err != nil {
		return fmt.Errorf("seed managed database: %w", err)
	}
	for groupName, level := range dbFixture.Grants {
		groupID, ok := groupIDs[groupName]
		if !ok {
			return fmt.Errorf("database grant refers to unknown group %s", groupName)
		}
		if _, err := platformPool.Exec(ctx, `
			INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)
			ON CONFLICT (group_id, database_id) DO UPDATE SET level = EXCLUDED.level`, groupID, databaseID, level); err != nil {
			return fmt.Errorf("seed database grant %s: %w", groupName, err)
		}
	}

	migrationID, err := seedMigration(ctx, platformPool, databaseID, userIDs["demo_editor"], migration)
	if err != nil {
		return err
	}

	fmt.Printf("seeded target database %s with %d customers\n", dbFixture.DBName, len(customers))
	fmt.Printf("seeded managed database %q (id %d)\n", dbFixture.Name, databaseID)
	fmt.Printf("seeded migration %q (id %d)\n", migration.Title, migrationID)
	fmt.Println("demo users:")
	for _, fixture := range users {
		fmt.Printf("  %s / %s\n", fixture.Username, fixture.Password)
	}
	return nil
}

func seedTarget(ctx context.Context, pool *pgxpool.Pool, customers []customerFixture) error {
	if _, err := pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS migration_worker;
		CREATE TABLE IF NOT EXISTS migration_worker.target_identity (
			singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
			id UUID NOT NULL DEFAULT gen_random_uuid()
		);
		INSERT INTO migration_worker.target_identity DEFAULT VALUES
		ON CONFLICT (singleton) DO NOTHING;
		CREATE TABLE IF NOT EXISTS migration_worker.completions (
			execution_id UUID PRIMARY KEY,
			attempt_id UUID NOT NULL,
			plan_hash TEXT NOT NULL,
			completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
		);
		CREATE TABLE IF NOT EXISTS migration_worker.batch_runs (
			execution_id UUID PRIMARY KEY,
			attempt_id UUID NOT NULL,
			plan_hash TEXT NOT NULL,
			scope_rows BIGINT NOT NULL DEFAULT 0,
			committed_batches BIGINT NOT NULL DEFAULT 0,
			committed_rows BIGINT NOT NULL DEFAULT 0,
			last_key BIGINT,
			CHECK (scope_rows >= committed_rows AND committed_rows >= committed_batches AND committed_batches >= 0),
			CHECK ((committed_batches = 0) = (last_key IS NULL))
		);
		CREATE TABLE IF NOT EXISTS migration_worker.batch_scope (
			execution_id UUID NOT NULL,
			key BIGINT NOT NULL,
			PRIMARY KEY (execution_id, key)
		);
		CREATE TABLE IF NOT EXISTS migration_worker.batch_checkpoints (
			execution_id UUID NOT NULL,
			sequence BIGINT NOT NULL,
			last_key BIGINT NOT NULL,
			affected_rows BIGINT NOT NULL CHECK (affected_rows > 0),
			PRIMARY KEY (execution_id, sequence)
		);
		CREATE TABLE IF NOT EXISTS migration_worker.batch_row_snapshots (
			execution_id UUID NOT NULL,
			key BIGINT NOT NULL,
			before_row JSONB NOT NULL,
			after_row JSONB,
			PRIMARY KEY (execution_id, key)
		);
		CREATE TABLE IF NOT EXISTS migration_worker.compensation_checkpoints (
			execution_id UUID NOT NULL,
			sequence BIGINT NOT NULL,
			recovery_execution_id UUID NOT NULL,
			attempt_id UUID NOT NULL,
			affected_rows BIGINT NOT NULL CHECK (affected_rows > 0),
			PRIMARY KEY (execution_id, sequence)
		)`); err != nil {
		return fmt.Errorf("create migration worker target schema: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS demo_customers (
			id BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			active BOOLEAN NOT NULL DEFAULT true,
			notes TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("create demo_customers: %w", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE demo_customers ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add demo_customers notes: %w", err)
	}
	for _, customer := range customers {
		if _, err := pool.Exec(ctx, `
			INSERT INTO demo_customers (id, name, email, active, notes)
			VALUES ($1, $2, $3, $4, '')
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				email = EXCLUDED.email,
				active = EXCLUDED.active`, customer.ID, customer.Name, customer.Email, customer.Active); err != nil {
			return fmt.Errorf("seed customer %d: %w", customer.ID, err)
		}
	}
	return nil
}

func seedMigration(ctx context.Context, pool *pgxpool.Pool, databaseID, createdBy int64, fixture migrationFixture) (int64, error) {
	var id int64
	var status string
	err := pool.QueryRow(ctx, `SELECT id, status FROM migrations WHERE database_id=$1 AND title=$2 ORDER BY id LIMIT 1`, databaseID, fixture.Title).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.QueryRow(ctx, `
			INSERT INTO migrations (database_id, title, sql, down_sql, status, created_by)
			VALUES ($1, $2, $3, $4, 'draft', $5)
			RETURNING id`, databaseID, fixture.Title, fixture.SQL, fixture.DownSQL, createdBy).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("create demo migration: %w", err)
		}
		return id, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find demo migration: %w", err)
	}
	if status == "draft" {
		if _, err := pool.Exec(ctx, `
			UPDATE migrations SET sql=$1, down_sql=$2, created_by=$3, revision=revision+1
			WHERE id=$4 AND database_id=$5
			  AND (sql IS DISTINCT FROM $1 OR down_sql IS DISTINCT FROM $2)`,
			fixture.SQL, fixture.DownSQL, createdBy, id, databaseID); err != nil {
			return 0, fmt.Errorf("update demo migration: %w", err)
		}
	}
	return id, nil
}

func ensureDatabase(ctx context.Context, platformURL, name string) error {
	if !databaseNamePattern.MatchString(name) {
		return fmt.Errorf("invalid target database name %q", name)
	}
	adminURL, err := databaseURL(platformURL, "postgres")
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect to postgres admin database: %w", err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("check target database: %w", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
			return fmt.Errorf("create target database %s: %w", name, err)
		}
	}
	return nil
}

func databaseURL(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse database URL: %w", err)
	}
	u.Path = "/" + name
	u.RawPath = ""
	return u.String(), nil
}

func encryptionKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY"))
	if raw == "" {
		return nil, errors.New("ENCRYPTION_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be base64 for exactly 32 bytes")
	}
	return key, nil
}

func readJSON[T any](dir, name string) (T, error) {
	var value T
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return value, fmt.Errorf("read fixture %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	return value, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
