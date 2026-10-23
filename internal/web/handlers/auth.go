package handlers

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

type Authenticator interface {
	Login(ctx context.Context, username, password string) (access.User, error)
}

type SessionManager interface {
	Login(ctx context.Context, userID int64) (string, error)
	Logout(ctx context.Context, rawToken string) error
}

type AuthHandler struct {
	auth     Authenticator
	sessions SessionManager
	cookie   session.CookieConfig
}

func NewAuthHandler(auth Authenticator, sessions SessionManager, cookie session.CookieConfig) *AuthHandler {
	return &AuthHandler{auth: auth, sessions: sessions, cookie: cookie}
}

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Login</title></head>
<body>
{{if .Error}}<p>{{.Error}}</p>{{end}}
<form method="post" action="/login">
	<label>Username <input name="username" autocomplete="username"></label>
	<label>Password <input type="password" name="password" autocomplete="current-password"></label>
	<button type="submit">Log in</button>
</form>
</body>
</html>`))

func (h *AuthHandler) renderLogin(w http.ResponseWriter, status int, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := loginTmpl.Execute(w, struct{ Error string }{errMsg}); err != nil {
		log.Printf("login: render: %v", err)
	}
}

func (h *AuthHandler) PostLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	user, err := h.auth.Login(ctx, username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.renderLogin(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	if err != nil {
		log.Printf("login: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if old, ok := session.ReadCookie(r, h.cookie); ok {
		if err := h.sessions.Logout(ctx, old); err != nil && !errors.Is(err, session.ErrUnauthenticated) {
			log.Printf("login: revoke previous session: %v", err)
		}
	}

	raw, err := h.sessions.Login(ctx, user.ID)
	if err != nil {
		log.Printf("login: create session: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	session.SetCookie(w, h.cookie, raw, time.Time{})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if raw, ok := session.ReadCookie(r, h.cookie); ok {
		err := h.sessions.Logout(ctx, raw)
		if err != nil {
			log.Printf("logout: %v", err)
			// no return, keep user logged out
		}
	}
		session.ClearCookie(w, h.cookie)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) GetLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, ok := access.UserFromContext(ctx); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderLogin(w, http.StatusOK, "")
}
