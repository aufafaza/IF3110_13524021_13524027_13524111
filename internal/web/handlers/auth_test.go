package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
)

const cookieName = "sid"

type fakeAuth struct {
	user access.User
	err  error

	gotUsername, gotPassword string
}

func (f *fakeAuth) Login(_ context.Context, username, password string) (access.User, error) {
	f.gotUsername, f.gotPassword = username, password
	return f.user, f.err
}

type fakeSessions struct {
	rawToken  string
	loginErr  error
	logoutErr error

	loginUserIDs []int64
	loggedOut    []string
}

func (f *fakeSessions) Login(_ context.Context, userID int64) (string, error) {
	f.loginUserIDs = append(f.loginUserIDs, userID)
	return f.rawToken, f.loginErr
}

func (f *fakeSessions) Logout(_ context.Context, raw string) error {
	f.loggedOut = append(f.loggedOut, raw)
	return f.logoutErr
}

func newHandler(a *fakeAuth, s *fakeSessions) *handlers.AuthHandler {
	cfg := session.CookieConfig{Name: cookieName, Secure: true, SameSite: http.SameSiteLaxMode}
	return handlers.NewAuthHandler(a, s, cfg)
}

func postForm(path string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func findCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func TestPostLogin_Success(t *testing.T) {
	a := &fakeAuth{user: access.User{ID: 7, Username: "alice"}}
	s := &fakeSessions{rawToken: "new-token"}
	h := newHandler(a, s)

	rec := httptest.NewRecorder()
	h.PostLogin(rec, postForm("/login", url.Values{"username": {"alice"}, "password": {"secret"}}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	if a.gotUsername != "alice" || a.gotPassword != "secret" {
		t.Errorf("credentials passed = %q/%q", a.gotUsername, a.gotPassword)
	}
	if len(s.loginUserIDs) != 1 || s.loginUserIDs[0] != 7 {
		t.Errorf("session created for %v, want [7]", s.loginUserIDs)
	}

	c := findCookie(rec)
	if c == nil {
		t.Fatal("no session cookie set")
	}
	if c.Value != "new-token" {
		t.Errorf("cookie value = %q, want new-token", c.Value)
	}
	if !c.HttpOnly || !c.Secure {
		t.Errorf("cookie flags HttpOnly=%v Secure=%v, want both true", c.HttpOnly, c.Secure)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
}

func TestPostLogin_InvalidCredentials(t *testing.T) {
	a := &fakeAuth{err: auth.ErrInvalidCredentials}
	s := &fakeSessions{rawToken: "new-token"}
	h := newHandler(a, s)

	rec := httptest.NewRecorder()
	h.PostLogin(rec, postForm("/login", url.Values{"username": {"alice"}, "password": {"wrong"}}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Invalid username or password") {
		t.Error("generic error message missing from body")
	}
	if strings.Contains(body, "wrong") {
		t.Error("submitted password echoed in response body")
	}
	if len(s.loginUserIDs) != 0 {
		t.Error("session created for failed login")
	}
	if findCookie(rec) != nil {
		t.Error("cookie set for failed login")
	}
}

func TestPostLogin_InternalErrors(t *testing.T) {
	tests := []struct {
		name string
		a    *fakeAuth
		s    *fakeSessions
	}{
		{"auth store failure", &fakeAuth{err: errors.New("db down")}, &fakeSessions{}},
		{
			"session creation failure",
			&fakeAuth{user: access.User{ID: 7}},
			&fakeSessions{loginErr: errors.New("db down")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandler(tt.a, tt.s)

			rec := httptest.NewRecorder()
			h.PostLogin(rec, postForm("/login", url.Values{"username": {"alice"}, "password": {"secret"}}))

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}
			if strings.Contains(rec.Body.String(), "db down") {
				t.Error("internal error detail leaked in response body")
			}
			if findCookie(rec) != nil {
				t.Error("cookie set on failure")
			}
		})
	}
}

func TestPostLogin_RevokesPreviousSession(t *testing.T) {
	a := &fakeAuth{user: access.User{ID: 7}}
	s := &fakeSessions{rawToken: "new-token"}
	h := newHandler(a, s)

	req := postForm("/login", url.Values{"username": {"alice"}, "password": {"secret"}})
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "old-token"})
	rec := httptest.NewRecorder()
	h.PostLogin(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(s.loggedOut) != 1 || s.loggedOut[0] != "old-token" {
		t.Errorf("previous session revoked = %v, want [old-token]", s.loggedOut)
	}
}

func TestPostLogin_PreviousSessionRevokeFailureDoesNotBlockLogin(t *testing.T) {
	a := &fakeAuth{user: access.User{ID: 7}}
	s := &fakeSessions{rawToken: "new-token", logoutErr: errors.New("db down")}
	h := newHandler(a, s)

	req := postForm("/login", url.Values{"username": {"alice"}, "password": {"secret"}})
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "old-token"})
	rec := httptest.NewRecorder()
	h.PostLogin(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if c := findCookie(rec); c == nil || c.Value != "new-token" {
		t.Errorf("cookie = %+v, want new-token", c)
	}
}

func TestPostLogin_IgnoresQueryString(t *testing.T) {
	a := &fakeAuth{err: auth.ErrInvalidCredentials}
	h := newHandler(a, &fakeSessions{})

	req := postForm("/login?username=mallory&password=leaked", url.Values{})
	rec := httptest.NewRecorder()
	h.PostLogin(rec, req)

	if a.gotUsername != "" || a.gotPassword != "" {
		t.Errorf("credentials read from URL: %q/%q", a.gotUsername, a.gotPassword)
	}
}

func TestPostLogout_WithSession(t *testing.T) {
	s := &fakeSessions{}
	h := newHandler(&fakeAuth{}, s)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.PostLogout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	if len(s.loggedOut) != 1 || s.loggedOut[0] != "tok" {
		t.Errorf("revoked = %v, want [tok]", s.loggedOut)
	}
	c := findCookie(rec)
	if c == nil || c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", c)
	}
}

func TestPostLogout_RevokeFailureStillClearsCookie(t *testing.T) {
	s := &fakeSessions{logoutErr: errors.New("db down")}
	h := newHandler(&fakeAuth{}, s)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.PostLogout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if c := findCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", c)
	}
}

func TestPostLogout_NoCookie(t *testing.T) {
	s := &fakeSessions{}
	h := newHandler(&fakeAuth{}, s)

	rec := httptest.NewRecorder()
	h.PostLogout(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(s.loggedOut) != 0 {
		t.Errorf("Logout called without a cookie: %v", s.loggedOut)
	}
}

func TestGetLogin_Anonymous(t *testing.T) {
	h := newHandler(&fakeAuth{}, &fakeSessions{})

	rec := httptest.NewRecorder()
	h.GetLogin(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="username"`) || !strings.Contains(body, `name="password"`) {
		t.Error("login form fields missing")
	}
	if strings.Contains(body, "Invalid username or password") {
		t.Error("error message shown before any login attempt")
	}
}

func TestGetLogin_AlreadyLoggedIn(t *testing.T) {
	h := newHandler(&fakeAuth{}, &fakeSessions{})

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req = req.WithContext(access.ContextWithUser(req.Context(), access.User{ID: 7, Username: "alice"}))
	rec := httptest.NewRecorder()
	h.GetLogin(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	if strings.Contains(rec.Body.String(), `name="password"`) {
		t.Error("login form rendered after redirect")
	}
}
