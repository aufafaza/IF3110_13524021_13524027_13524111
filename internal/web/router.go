package web

import (
	"encoding/json"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/home"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migrationjob"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/static"
)

type Deps struct {
	Auth          *auth.Handler
	Home          *home.Handler
	Admin         *admin.Handler
	Authenticate  *middleware.Authenticator
	Guard         *access.Guard
	Databases     *database.Handler  // optional; nil leaves the database routes out
	Migrations    *migration.Handler // optional; nil leaves the migration routes out
	MigrationJobs *migrationjob.Handler
}

func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static.FS)))

	d.Auth.RegisterRoutes(mux)
	d.Home.RegisterRoutes(mux, d.Guard)
	d.Admin.RegisterRoutes(mux, d.Guard)
	if d.Databases != nil {
		d.Databases.RegisterRoutes(mux, d.Guard)
	}
	if d.Migrations != nil {
		d.Migrations.RegisterRoutes(mux)
	}

	if d.MigrationJobs != nil {
		d.MigrationJobs.RegisterRoutes(mux)
	}

	return observability.Middleware(d.Authenticate.Middleware(csrf.Protect(csrf.PreSessionCookie)(mux)))
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
