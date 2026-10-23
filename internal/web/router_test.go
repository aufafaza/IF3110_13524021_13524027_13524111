package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

const testCookie = "sid"

var testSecret = []byte("0123456789abcdef")

func formPost(target string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

type fakeSessions struct {
	valid     bool
	loggedOut []string
}

func (f *fakeSessions) Validate(_ context.Context, _ string) (session.Validated, error) {
	if !f.valid {
		return session.Validated{}, session.ErrUnauthenticated
	}
	return session.Validated{Session: session.Session{UserID: 7, CSRFSecret: testSecret}}, nil
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
	body := rec.Body.String()
	if !strings.Contains(body, "alice") {
		t.Error("username missing from home page")
	}
	if !strings.Contains(body, `<form method="post" action="/logout">`) {
		t.Error("logout form missing from home page")
	}
	wantField := `name="csrf_token" value="` + csrf.TokenFromSecret(testSecret) + `"`
	if !strings.Contains(body, wantField) {
		t.Errorf("home page missing hidden field %s", wantField)
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
	req := formPost("/logout", url.Values{"csrf_token": {csrf.TokenFromSecret(testSecret)}})
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

func TestLogout_WithoutCSRFTokenIsForbidden(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	req := formPost("/logout", url.Values{})
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	newTestRouter(sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if len(sessions.loggedOut) != 0 {
		t.Errorf("session revoked despite missing token: %v", sessions.loggedOut)
	}
}

func TestLogout_WithWrongCSRFTokenIsForbidden(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	req := formPost("/logout", url.Values{"csrf_token": {"forged"}})
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	newTestRouter(sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if len(sessions.loggedOut) != 0 {
		t.Errorf("session revoked despite wrong token: %v", sessions.loggedOut)
	}
}

func TestPostLogin_WithoutCSRFCookieIsForbidden(t *testing.T) {
	sessions := &fakeSessions{}
	req := formPost("/login", url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {"x"}})
	rec := httptest.NewRecorder()
	newTestRouter(sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestPostLogin_WithMatchingCSRFCookieSucceeds(t *testing.T) {
	sessions := &fakeSessions{}
	req := formPost("/login", url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {"pre"}})
	req.AddCookie(&http.Cookie{Name: csrf.PreSessionCookie, Value: "pre"})
	rec := httptest.NewRecorder()
	newTestRouter(sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestLogout_RejectsGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&fakeSessions{valid: true}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logout", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
