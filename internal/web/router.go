package web

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

type Deps struct {
	Auth         *handlers.AuthHandler
	Authenticate *middleware.Authenticator
	Guard        *access.Guard
}

func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)

	mux.HandleFunc("GET /login", d.Auth.GetLogin)
	mux.HandleFunc("POST /login", d.Auth.PostLogin)
	mux.HandleFunc("POST /logout", d.Auth.PostLogout)

	mux.Handle("GET /{$}", d.Guard.RequireLogin(http.HandlerFunc(home)))

	return d.Authenticate.Middleware(mux)
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
<form method="post" action="/logout"><button type="submit">Log out</button></form>
</body>
</html>`))

func home(w http.ResponseWriter, r *http.Request) {
	user, _ := access.UserFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homeTmpl.Execute(w, user); err != nil {
		log.Printf("home: render: %v", err)
	}
}
