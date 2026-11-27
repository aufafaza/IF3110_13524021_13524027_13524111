package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/home"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
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

type fakeUsers struct{ admin bool }

func (f fakeUsers) FindByID(_ context.Context, _ int64) (auth.StoredUser, error) {
	return auth.StoredUser{ID: 7, Username: "alice", IsActive: true, IsPlatformAdmin: f.admin}, nil
}

type fakeLogin struct{}

func (fakeLogin) Login(_ context.Context, _, _ string) (access.User, error) {
	return access.User{ID: 7, Username: "alice"}, nil
}

type fakeAdmin struct {
	admin.AdminService
	created []string
}

func (f *fakeAdmin) ListUsers(context.Context) ([]admin.User, error) {
	return []admin.User{{ID: 7, Username: "alice", IsActive: true, IsPlatformAdmin: true}}, nil
}

func (f *fakeAdmin) CreateUser(_ context.Context, username, _ string, _ bool) error {
	f.created = append(f.created, username)
	return nil
}

func (f *fakeAdmin) ListGroups(context.Context) ([]admin.Group, error) {
	return []admin.Group{{ID: 1, Name: "analysts", MemberCount: 2}}, nil
}

type testRig struct {
	handler  http.Handler
	sessions *fakeSessions
	admin    *fakeAdmin
}

func newRig(t *testing.T, sessions *fakeSessions, isAdmin bool) testRig {
	t.Helper()
	tmpl, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	adminSvc := &fakeAdmin{}
	cookie := session.CookieConfig{Name: testCookie, SameSite: http.SameSiteLaxMode}
	h := New(Deps{
		Auth:         auth.NewHandler(fakeLogin{}, sessions, cookie),
		Home:         home.NewHandler(tmpl),
		Admin:        admin.NewHandler(adminSvc, tmpl),
		Authenticate: middleware.NewAuthenticator(sessions, fakeUsers{admin: isAdmin}, cookie),
		Guard:        access.NewGuard(nil),
	})
	return testRig{handler: h, sessions: sessions, admin: adminSvc}
}

func newTestRouter(t *testing.T, sessions *fakeSessions) http.Handler {
	return newRig(t, sessions, false).handler
}

func withSession(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "tok"})
	return req
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestHome_AnonymousGetsJSONUnauthorized(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assertJSONAccessError(t, rec, http.StatusUnauthorized, "unauthenticated")
}

func TestHome_LoggedIn(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{valid: true}).ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, "/", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice") {
		t.Error("username missing from home page")
	}

	token := csrf.TokenFromSecret(testSecret)
	logoutForm := regexp.MustCompile(`(?s)<form[^>]*action="/logout"[^>]*>\s*<input type="hidden" name="csrf_token" value="` + regexp.QuoteMeta(token) + `">`)
	if !logoutForm.MatchString(body) {
		t.Error("the CSRF token is not inside the logout form")
	}
}

func TestHome_AdminLinksOnlyForAdmins(t *testing.T) {
	for name, isAdmin := range map[string]bool{"admin": true, "regular user": false} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newRig(t, &fakeSessions{valid: true}, isAdmin).handler.ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, "/", nil)))

			if got := strings.Contains(rec.Body.String(), "/admin/users"); got != isAdmin {
				t.Errorf("admin link present = %v, want %v", got, isAdmin)
			}
		})
	}
}

func TestLoginPage_IsPublic(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLoginPage_RedirectsWhenLoggedIn(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{valid: true}).ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, "/login", nil)))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestLogout_RevokesAndRedirects(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	req := withSession(formPost("/logout", url.Values{"csrf_token": {csrf.TokenFromSecret(testSecret)}}))
	rec := httptest.NewRecorder()
	newTestRouter(t, sessions).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if len(sessions.loggedOut) != 1 || sessions.loggedOut[0] != "tok" {
		t.Errorf("revoked = %v, want [tok]", sessions.loggedOut)
	}
}

func TestLogout_WithoutCSRFTokenIsForbidden(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	rec := httptest.NewRecorder()
	newTestRouter(t, sessions).ServeHTTP(rec, withSession(formPost("/logout", url.Values{})))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if len(sessions.loggedOut) != 0 {
		t.Errorf("session revoked despite missing token: %v", sessions.loggedOut)
	}
}

func TestLogout_WithWrongCSRFTokenIsForbidden(t *testing.T) {
	sessions := &fakeSessions{valid: true}
	rec := httptest.NewRecorder()
	newTestRouter(t, sessions).ServeHTTP(rec, withSession(formPost("/logout", url.Values{"csrf_token": {"forged"}})))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if len(sessions.loggedOut) != 0 {
		t.Errorf("session revoked despite wrong token: %v", sessions.loggedOut)
	}
}

func TestLogout_RejectsGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{valid: true}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logout", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestPostLogin_WithoutCSRFCookieIsForbidden(t *testing.T) {
	req := formPost("/login", url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {"x"}})
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestPostLogin_WithMatchingCSRFCookieSucceeds(t *testing.T) {
	req := formPost("/login", url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {"pre"}})
	req.AddCookie(&http.Cookie{Name: csrf.PreSessionCookie, Value: "pre"})
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

var adminPages = []string{"/admin/users", "/admin/groups", "/admin/groups/1"}

func TestAdminPages_AnonymousGetsJSONUnauthorized(t *testing.T) {
	h := newRig(t, &fakeSessions{}, false).handler
	for _, path := range adminPages {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			assertJSONAccessError(t, rec, http.StatusUnauthorized, "unauthenticated")
		})
	}
}

func TestAdminPages_NonAdminIsForbidden(t *testing.T) {
	h := newRig(t, &fakeSessions{valid: true}, false).handler
	for _, path := range adminPages {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, path, nil)))

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestAdminPages_AdminCanView(t *testing.T) {
	h := newRig(t, &fakeSessions{valid: true}, true).handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, "/admin/users", nil)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice") {
		t.Errorf("users page: status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, "/admin/groups", nil)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "analysts") {
		t.Errorf("groups page: status = %d", rec.Code)
	}
}

func TestAdminMutations_AuthorizationAndCSRF(t *testing.T) {
	validToken := csrf.TokenFromSecret(testSecret)
	form := url.Values{"username": {"newuser"}, "password": {"a-long-enough-password"}}

	tests := []struct {
		name        string
		valid       bool
		isAdmin     bool
		build       func() *http.Request
		wantStatus  int
		wantCreated int
	}{
		{
			"admin with form token", true, true,
			func() *http.Request {
				f := url.Values{"username": form["username"], "password": form["password"], "csrf_token": {validToken}}
				return withSession(formPost("/admin/users", f))
			},
			http.StatusOK, 1,
		},
		{
			"admin with header token", true, true,
			func() *http.Request {
				req := withSession(formPost("/admin/users", form))
				req.Header.Set("X-CSRF-Token", validToken)
				return req
			},
			http.StatusOK, 1,
		},
		{
			"admin without token", true, true,
			func() *http.Request { return withSession(formPost("/admin/users", form)) },
			http.StatusForbidden, 0,
		},
		{
			"admin with forged token", true, true,
			func() *http.Request {
				req := withSession(formPost("/admin/users", form))
				req.Header.Set("X-CSRF-Token", "forged")
				return req
			},
			http.StatusForbidden, 0,
		},
		{
			"non-admin with valid token", true, false,
			func() *http.Request {
				req := withSession(formPost("/admin/users", form))
				req.Header.Set("X-CSRF-Token", validToken)
				return req
			},
			http.StatusForbidden, 0,
		},
		{
			"anonymous", false, false,
			func() *http.Request { return formPost("/admin/users", form) },
			http.StatusForbidden, 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t, &fakeSessions{valid: tt.valid}, tt.isAdmin)
			rec := httptest.NewRecorder()
			rig.handler.ServeHTTP(rec, tt.build())

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if len(rig.admin.created) != tt.wantCreated {
				t.Errorf("users created = %v, want %d", rig.admin.created, tt.wantCreated)
			}
		})
	}
}

func TestAdminRoutes_RejectWrongMethods(t *testing.T) {
	h := newRig(t, &fakeSessions{valid: true}, true).handler
	for _, path := range []string{"/admin/users/7/active", "/admin/groups/1/delete", "/admin/groups/1/members"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, withSession(httptest.NewRequest(http.MethodGet, path, nil)))

			if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 405 or 404", path, rec.Code)
			}
		})
	}
}

func TestStatic_ServedWithoutLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, &fakeSessions{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "css") {
		t.Errorf("Content-Type = %q, want CSS", contentType)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty stylesheet")
	}
}

func assertJSONAccessError(t *testing.T, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if value := rec.Header().Get("Location"); value != "" {
		t.Errorf("unexpected Location: %q", value)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON error: %v", err)
	}
	if payload.Error != message {
		t.Errorf("error = %q, want %q", payload.Error, message)
	}
}
