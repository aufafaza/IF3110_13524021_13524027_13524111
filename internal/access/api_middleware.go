package access

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
)

func RequireDBAccess(checker DatabaseChecker, min Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok || user.ID == 0 {
				WriteAPIError(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			dbIDStr := r.PathValue(DatabaseIDParam)
			dbID, err := strconv.ParseInt(dbIDStr, 10, 64)
			if err != nil || dbID <= 0 {
				WriteAPIError(w, "invalid database id", http.StatusBadRequest)
				return
			}

			r = r.WithContext(observability.WithDatabase(r.Context(), dbID))
			level, err := checker.EffectiveLevel(r.Context(), user.ID, dbID)
			if err != nil {
				WriteAPIError(w, "failed to verify database access", http.StatusInternalServerError)
				return
			}
			if level < min {
				WriteAPIError(w, "forbidden: insufficient database permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func WriteAPIError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
