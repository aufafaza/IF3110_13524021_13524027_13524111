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

func NewGuard(checker Checker) *Guard {
	return &Guard{checker: checker, loginPath: "/login"}
}

func (g *Guard) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); !ok {
			// return json error
			WriteAPIError(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			// return json error
			WriteAPIError(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !user.IsPlatformAdmin {

			WriteAPIError(w, "forbidden", http.StatusForbidden)
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
				// return json

				WriteAPIError(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			dbID, err := strconv.ParseInt(r.PathValue(DatabaseIDParam), 10, 64)
			if err != nil {

				WriteAPIError(w, "not found", http.StatusNotFound)
				return
			}

			level, err := g.checker.EffectiveLevel(r.Context(), user.ID, dbID)
			if err != nil {
				log.Printf("access: effective level: %v", err)

				WriteAPIError(w, "internal error", http.StatusInternalServerError)
				return
			}

			switch {
			case level == LevelNone:

				WriteAPIError(w, "not found", http.StatusNotFound)
			case level < min:
				WriteAPIError(w, "forbidden", http.StatusForbidden)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
