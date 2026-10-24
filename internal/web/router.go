package web

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

type Deps struct {
	Auth         *handlers.AuthHandler
	Authenticate *middleware.Authenticator
	Guard        *access.Guard
	Databases    *handlers.DatabaseHandler
}

func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)

	mux.HandleFunc("GET /login", d.Auth.GetLogin)
	mux.HandleFunc("POST /login", d.Auth.PostLogin)
	mux.HandleFunc("POST /logout", d.Auth.PostLogout)

	mux.Handle("GET /{$}", d.Guard.RequireLogin(http.HandlerFunc(home)))

	registerDatabaseRoutes(mux, d)

	return d.Authenticate.Middleware(csrf.Protect("csrf_login")(mux))
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

var homeTmpl = template.Must(template.New("home").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Home</title></head>
<body>
<p>Signed in as {{.Username}}{{if .IsPlatformAdmin}} (platform admin){{end}}</p>
<p><a href="/databases">Databases</a>{{if .IsPlatformAdmin}} | <a href="/admin/databases">Manage databases</a>{{end}}</p>
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<form method="post" action="/logout"><button type="submit">Log out</button></form>
</body>
</html>`))

func home(w http.ResponseWriter, r *http.Request) {
	user, _ := access.UserFromContext(r.Context())
	token, _ := csrf.TokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Username        string
		IsPlatformAdmin bool
		CSRFToken       string
	}{user.Username, user.IsPlatformAdmin, token}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homeTmpl.Execute(w, data); err != nil {
		log.Printf("home: render: %v", err)
	}
}
