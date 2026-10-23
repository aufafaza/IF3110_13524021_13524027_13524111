package web

import (
	"encoding/json"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/static"
)

type Deps struct {
	Auth         *handlers.AuthHandler
	Home         *handlers.HomeHandler
	Admin        *handlers.AdminHandler
	Authenticate *middleware.Authenticator
	Guard        *access.Guard
}

func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static.FS)))

	mux.HandleFunc("GET /login", d.Auth.GetLogin)
	mux.HandleFunc("POST /login", d.Auth.PostLogin)
	mux.HandleFunc("POST /logout", d.Auth.PostLogout)

	mux.Handle("GET /{$}", d.Guard.RequireLogin(http.HandlerFunc(d.Home.Index)))

	adminRoute := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, d.Guard.RequireAdmin(h))
	}
	adminRoute("GET /admin/users", d.Admin.UsersPage)
	adminRoute("POST /admin/users", d.Admin.CreateUser)
	adminRoute("POST /admin/users/{id}/active", d.Admin.SetUserActive)
	adminRoute("POST /admin/users/{id}/password", d.Admin.ResetPassword)

	adminRoute("GET /admin/groups", d.Admin.GroupsPage)
	adminRoute("POST /admin/groups", d.Admin.CreateGroup)
	adminRoute("GET /admin/groups/{id}", d.Admin.GroupPage)
	adminRoute("POST /admin/groups/{id}/delete", d.Admin.DeleteGroup)
	adminRoute("POST /admin/groups/{id}/members", d.Admin.AddMember)
	adminRoute("POST /admin/groups/{id}/members/{userID}/remove", d.Admin.RemoveMember)
	adminRoute("POST /admin/groups/{id}/grants", d.Admin.SetGrant)
	adminRoute("POST /admin/groups/{id}/grants/{dbID}/remove", d.Admin.RemoveGrant)

	return d.Authenticate.Middleware(csrf.Protect(csrf.PreSessionCookie)(mux))
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
