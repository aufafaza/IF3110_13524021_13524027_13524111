package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		log.Fatal(err)
	}

	userStore := auth.NewPostgresStore(pool)
	authService, err := auth.NewService(userStore, auth.NewArgon2idHasher())
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	sessions := session.NewManager(session.NewPostgresStore(pool), session.RealClock{}, session.Config{
		IdleTimeout:      cfg.Session.IdleTimeout,
		AbsoluteTimeout:  cfg.Session.AbsoluteTimeout,
		RotationInterval: cfg.Session.RotationInterval,
		GracePeriod:      cfg.Session.GracePeriod,
	})

	cookie := session.CookieConfig{
		Name:     cfg.Session.CookieName,
		Secure:   cfg.Session.Secure,
		SameSite: session.ParseSameSite(cfg.Session.SameSite),
		Domain:   cfg.Session.Domain,
	}

	cipher, err := dbmanager.NewCipher(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("dbmanager: %v", err)
	}

	registry := dbmanager.NewRegistry(pool, cipher)
	defer registry.Close()

	tmpl, err := templates.New()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	checker := access.NewPostgresChecker(pool)

	adminService := admin.NewService(admin.NewPostgresStore(pool), auth.NewArgon2idHasher(), sessions)

	migrationRepo := migration.NewPostgresRepository(pool)
	migrationRunner := migration.NewRunner(
		migrationRepo,
		migration.NewTargetPostgresLocker(registry),
		migration.NewTargetPostgresExecutor(registry),
	)
	migrationService := migration.NewService(migrationRepo, migrationRunner, migration.AdaptChecker(checker))

	handler := web.New(web.Deps{
		Auth:         handlers.NewAuthHandler(authService, sessions, cookie),
		Home:         handlers.NewHomeHandler(tmpl),
		Admin:        handlers.NewAdminHandler(adminService, tmpl),
		Authenticate: middleware.NewAuthenticator(sessions, userStore, cookie),
		Guard:        access.NewGuard(checker, "/login"),
		Databases:    handlers.NewDatabaseHandler(registry, dbmanager.NewViewer(registry), checker, nil, tmpl),
		Migrations:   migration.NewHandler(migrationService),
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", cfg.ListenAddr)
	log.Fatal(srv.ListenAndServe())
}
