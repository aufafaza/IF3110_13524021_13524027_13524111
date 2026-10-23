package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
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

	handler := web.New(web.Deps{
		Auth:         handlers.NewAuthHandler(authService, sessions, cookie),
		Authenticate: middleware.NewAuthenticator(sessions, userStore, cookie),
		Guard:        access.NewGuard(access.NewPostgresChecker(pool), "/login"),
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", cfg.ListenAddr)
	log.Fatal(srv.ListenAndServe())
}
