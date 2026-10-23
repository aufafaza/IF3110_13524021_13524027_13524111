package access

import (
	"log"
	"net/http"
	"strconv"
)

const DatabaseIDParam = "dbID"

type Guard struct {
	checker   Checker
	loginPath string
}

func NewGuard(checker Checker, loginPath string) *Guard {
	return &Guard{checker: checker, loginPath: loginPath}
}

func (g *Guard) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); !ok {
			http.Redirect(w, r, g.loginPath, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, g.loginPath, http.StatusSeeOther)
			return
		}
		if !user.IsPlatformAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) RequireDBAccess(min Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				http.Redirect(w, r, g.loginPath, http.StatusSeeOther)
				return
			}

			dbID, err := strconv.ParseInt(r.PathValue(DatabaseIDParam), 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			level, err := g.checker.EffectiveLevel(r.Context(), user.ID, dbID)
			if err != nil {
				log.Printf("access: effective level: %v", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			switch {
			case level == LevelNone:
				http.NotFound(w, r)
			case level < min:
				http.Error(w, "forbidden", http.StatusForbidden)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
