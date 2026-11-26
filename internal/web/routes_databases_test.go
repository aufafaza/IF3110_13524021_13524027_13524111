package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type noLevelChecker struct{}

func (noLevelChecker) IsPlatformAdmin(context.Context, int64) (bool, error) { return false, nil }
func (noLevelChecker) EffectiveLevel(context.Context, int64, int64) (access.Level, error) {
	return access.LevelNone, nil
}

func databaseRouter(t *testing.T, valid bool) http.Handler {
	t.Helper()
	tmpl, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{valid: valid}
	cookie := session.CookieConfig{Name: testCookie, SameSite: http.SameSiteLaxMode}
	return New(Deps{
		Auth:         auth.NewHandler(fakeLogin{}, sessions, cookie),
		Authenticate: middleware.NewAuthenticator(sessions, fakeUsers{}, cookie),
		Guard:        access.NewGuard(noLevelChecker{}),
		Databases:    database.NewHandler(nil, nil, noLevelChecker{}, nil, tmpl),
	})
}

func TestDatabaseRoutes_AnonymousGetsJSONUnauthorized(t *testing.T) {
	for _, path := range []string{"/api/databases", "/api/databases/1/tables", "/api/databases/1/table?schema=a&table=b", "/api/databases/1/table/rows?schema=a&table=b", "/admin/databases"} {
		rec := httptest.NewRecorder()
		databaseRouter(t, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assertJSONAccessError(t, rec, http.StatusUnauthorized, "unauthenticated")
	}
}

func TestDatabaseRoutes_NonAdminCannotManage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/databases", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	databaseRouter(t, true).ServeHTTP(rec, req)
	assertJSONAccessError(t, rec, http.StatusForbidden, "forbidden")
}

func TestDatabaseRoutes_NoGrantIsForbidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/databases/1/tables", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	databaseRouter(t, true).ServeHTTP(rec, req)
	assertJSONAccessError(t, rec, http.StatusForbidden, "forbidden: insufficient database permissions")
}
