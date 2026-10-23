package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

const testCookie = "sid"

type fakeSessions struct {
	valid     bool
	loggedOut []string
}

func (f *fakeSessions) Validate(_ context.Context, _ string) (session.Session, error) {
	if !f.valid {
		return session.Session{}, session.ErrUnauthenticated
	}
	return session.Session{UserID: 7}, nil
}

func (f *fakeSessions) Login(_ context.Context, _ int64) (string, error) { return "tok", nil }

func (f *fakeSessions) Logout(_ context.Context, raw string) error {
	f.loggedOut = append(f.loggedOut, raw)
	return nil
}

type fakeUsers struct{}

func (fakeUsers) FindByID(_ context.Context, _ int64) (auth.StoredUser, error) {
	return auth.StoredUser{ID: 7, Username: "alice", IsActive: true}, nil
}

type fakeLogin struct{}

func (fakeLogin) Login(_ context.Context, _, _ string) (access.User, error) {
	return access.User{ID: 7, Username: "alice"}, nil
}

func newTestRouter(sessions *fakeSessions) http.Handler {
	cookie := session.CookieConfig{Name: testCookie, SameSite: http.SameSiteLaxMode}
	return New(Deps{
		Auth:         handlers.NewAuthHandler(fakeLogin{}, sessions, cookie),
		Authenticate: middleware.NewAuthenticator(sessions, fakeUsers{}, cookie),
		Guard:        access.NewGuard(nil, "/login"),
	})
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestHome_AnonymousRedirectsToLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestHome_LoggedInShowsUsername(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{valid: true}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "alice") {
		t.Error("username missing from home page")
	}
}

func TestLoginPage_IsPublic(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLoginPage_RedirectsWhenLoggedIn(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{valid: true}).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestLogout_RevokesAndRedirects(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	newTestRouter(sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(sessions.loggedOut) != 1 || sessions.loggedOut[0] != "tok" {
		t.Errorf("revoked = %v, want [tok]", sessions.loggedOut)
	}
}

func TestLogout_RejectsGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{valid: true}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logout", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
