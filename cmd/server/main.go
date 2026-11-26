package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/home"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migrationjob"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func main() {
	observability.ConfigureJSON(os.Stdout)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	cipher, err := database.NewCipher(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("dbmanager: %v", err)
	}

	registry := database.NewRegistry(pool, cipher)
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
	approvalRepo := migration.NewPostgresApprovalRepository(pool)
	migrationService := migration.NewServiceWithApprovals(migrationRepo, migrationRunner, checker, approvalRepo)
	jobStore := migrationjob.NewStore(pool)
	queue := migrationjob.NewReviewQueue(jobStore, approvalRepo)
	worker := migrationjob.NewApprovedWorker(jobStore, registry, migrationService)

	handler := web.New(web.Deps{
		Auth:          auth.NewHandler(authService, sessions, cookie),
		Home:          home.NewHandler(tmpl),
		Admin:         admin.NewHandler(adminService, tmpl),
		Authenticate:  middleware.NewAuthenticator(sessions, userStore, cookie),
		Guard:         access.NewGuard(checker),
		Databases:     database.NewHandler(registry, database.NewViewer(registry), checker, nil, tmpl),
		Migrations:    migration.NewQueuedHandler(migrationService, queue),
		MigrationJobs: migrationjob.NewHandler(jobStore, checker),
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	var workers sync.WaitGroup
	workers.Go(func() { queue.Run(ctx) })
	for range 2 {
		workers.Go(func() { worker.Run(ctx) })
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s", cfg.ListenAddr)
	err = srv.ListenAndServe()
	stop()
	<-shutdownDone
	workers.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
