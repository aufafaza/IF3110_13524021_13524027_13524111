package middleware

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

type UserLookup interface {
	FindByID(ctx context.Context, id int64) (auth.StoredUser, error)
}

type SessionManager interface {
	Validate(ctx context.Context, rawToken string) (session.Session, error)
	Logout(ctx context.Context, rawToken string) error
}

type Authenticator struct {
	sessions SessionManager
	users    UserLookup
	cookie   session.CookieConfig
}

func NewAuthenticator(sessions SessionManager, users UserLookup, cookie session.CookieConfig) *Authenticator {
	return &Authenticator{sessions: sessions, users: users, cookie: cookie}
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		raw, ok := session.ReadCookie(r, a.cookie)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		sess, err := a.sessions.Validate(ctx, raw)
		if errors.Is(err, session.ErrUnauthenticated) {
			session.ClearCookie(w, a.cookie)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			log.Printf("authenticate: validate session: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		stored, err := a.users.FindByID(ctx, sess.UserID)
		if errors.Is(err, auth.ErrNotFound) {
			a.dropSession(ctx, w, raw)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			log.Printf("authenticate: load user: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if !stored.IsActive {
			a.dropSession(ctx, w, raw)
			next.ServeHTTP(w, r)
			return
		}

		user := access.User{
			ID:              stored.ID,
			Username:        stored.Username,
			IsPlatformAdmin: stored.IsPlatformAdmin,
		}
		ctx = csrf.ContextWithToken(ctx, csrf.TokenFromSecret(sess.CSRFSecret))
		ctx = access.ContextWithUser(ctx, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Authenticator) dropSession(ctx context.Context, w http.ResponseWriter, raw string) {
	if err := a.sessions.Logout(ctx, raw); err != nil {
		log.Printf("authenticate: revoke session: %v", err)
	}
	session.ClearCookie(w, a.cookie)
}
