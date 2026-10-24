package main 

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

const minPasswordLength = 3

func run(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("-username is required")
	}

	password, err := readPassword()

	if err != nil {
		return err
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be >= 3")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		return err

	}

	hash, err := auth.NewArgon2idHasher().Hash(password)
	if err != nil {
		return err
	}

	id, err := auth.NewPostgresStore(pool).Create(ctx, auth.CreateUserParams{
		Username:        username,
		PasswordHash:    hash,
		IsPlatformAdmin: true,
	})

	if errors.Is(err, auth.ErrUsernameTaken) {
		return fmt.Errorf("user %q already exists", username)
	}
	if err != nil {
		return err
	}

	fmt.Printf("created platform admin %q (id %d)\n", username, id)
	return nil
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

func main() {
	username := flag.String("username", "", "username of the platform admin to create")
	flag.Parse()

	if err := run(*username); err != nil {
		fmt.Fprintln(os.Stderr, "seed-admin:", err)
		os.Exit(1)
	}
}
