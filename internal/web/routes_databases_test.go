package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
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
		Auth:         handlers.NewAuthHandler(fakeLogin{}, sessions, cookie),
		Authenticate: middleware.NewAuthenticator(sessions, fakeUsers{}, cookie),
		Guard:        access.NewGuard(noLevelChecker{}, "/login"),
		Databases:    handlers.NewDatabaseHandler(nil, nil, noLevelChecker{}, nil, tmpl),
	})
}

func TestDatabaseRoutes_AnonymousRedirectsToLogin(t *testing.T) {
	for _, path := range []string{"/databases", "/databases/1", "/databases/1/table?schema=a&table=b", "/admin/databases"} {
		rec := httptest.NewRecorder()
		databaseRouter(t, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s: status %d, want 303", path, rec.Code)
		}
	}
}

func TestDatabaseRoutes_NonAdminCannotManage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/databases", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	databaseRouter(t, true).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
}

func TestDatabaseRoutes_NoGrantIs404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/databases/1", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	databaseRouter(t, true).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}