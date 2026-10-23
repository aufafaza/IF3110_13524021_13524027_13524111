package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

const cookieName = "sid"

var csrfSecret = []byte("0123456789abcdef")

type fakeSessions struct {
	sess        session.Session
	validateErr error
	logoutErr   error
	loggedOut   []string
}

func (f *fakeSessions) Validate(_ context.Context, _ string) (session.Session, error) {
	return f.sess, f.validateErr
}

func (f *fakeSessions) Logout(_ context.Context, raw string) error {
	f.loggedOut = append(f.loggedOut, raw)
	return f.logoutErr
}

type fakeUsers struct {
	user auth.StoredUser
	err  error
}

func (f *fakeUsers) FindByID(_ context.Context, _ int64) (auth.StoredUser, error) {
	return f.user, f.err
}

type result struct {
	rec        *httptest.ResponseRecorder
	nextCalled bool
	user       access.User
	hasUser    bool
	token      string
	hasToken   bool
}

func run(t *testing.T, sessions *fakeSessions, users *fakeUsers, withCookie bool) result {
	t.Helper()

	cfg := session.CookieConfig{Name: cookieName, Secure: true, SameSite: http.SameSiteLaxMode}
	a := middleware.NewAuthenticator(sessions, users, cfg)

	var res result
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res.nextCalled = true
		res.user, res.hasUser = access.UserFromContext(r.Context())
		res.token, res.hasToken = csrf.TokenFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if withCookie {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: "raw-token"})
	}
	res.rec = httptest.NewRecorder()
	a.Middleware(next).ServeHTTP(res.rec, req)
	return res
}

func clearsCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value == "" && c.MaxAge < 0 {
			return true
		}
	}
	return false
}

func activeUser() auth.StoredUser {
	return auth.StoredUser{ID: 7, Username: "alice", IsPlatformAdmin: true, IsActive: true}
}

func TestAuthenticate_NoCookie(t *testing.T) {
	sessions := &fakeSessions{}
	res := run(t, sessions, &fakeUsers{}, false)

	if !res.nextCalled {
		t.Fatal("next handler was not called")
	}
	if res.hasUser {
		t.Error("user in context without a cookie")
	}
	if res.hasToken {
		t.Error("csrf token in context without a cookie")
	}
	if clearsCookie(res.rec) {
		t.Error("cookie cleared although none was sent")
	}
}

func TestAuthenticate_ValidSession(t *testing.T) {
	sessions := &fakeSessions{sess: session.Session{UserID: 7, CSRFSecret: csrfSecret}}
	users := &fakeUsers{user: activeUser()}

	res := run(t, sessions, users, true)

	if !res.nextCalled {
		t.Fatal("next handler was not called")
	}
	if !res.hasUser {
		t.Fatal("no user in context")
	}
	want := access.User{ID: 7, Username: "alice", IsPlatformAdmin: true}
	if res.user != want {
		t.Errorf("user = %+v, want %+v", res.user, want)
	}
	if !res.hasToken {
		t.Fatal("no csrf token in context")
	}
	if wantToken := csrf.TokenFromSecret(csrfSecret); res.token != wantToken {
		t.Errorf("csrf token = %q, want %q", res.token, wantToken)
	}
	if clearsCookie(res.rec) {
		t.Error("cookie cleared for a valid session")
	}
}

func TestAuthenticate_UnauthenticatedSession(t *testing.T) {
	sessions := &fakeSessions{validateErr: session.ErrUnauthenticated}
	res := run(t, sessions, &fakeUsers{user: activeUser()}, true)

	if !res.nextCalled {
		t.Fatal("next handler was not called")
	}
	if res.hasUser {
		t.Error("user in context for an invalid session")
	}
	if res.hasToken {
		t.Error("csrf token in context for an invalid session")
	}
	if !clearsCookie(res.rec) {
		t.Error("cookie was not cleared")
	}
}

func TestAuthenticate_UserGone(t *testing.T) {
	tests := []struct {
		name  string
		users *fakeUsers
	}{
		{"user not found", &fakeUsers{err: auth.ErrNotFound}},
		{"user inactive", &fakeUsers{user: auth.StoredUser{ID: 7, Username: "alice", IsActive: false}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeSessions{sess: session.Session{UserID: 7}}
			res := run(t, sessions, tt.users, true)

			if !res.nextCalled {
				t.Fatal("next handler was not called")
			}
			if res.hasUser {
				t.Error("user in context")
			}
			if res.hasToken {
				t.Error("csrf token in context")
			}
			if !clearsCookie(res.rec) {
				t.Error("cookie was not cleared")
			}
			if len(sessions.loggedOut) != 1 || sessions.loggedOut[0] != "raw-token" {
				t.Errorf("session not revoked, loggedOut = %v", sessions.loggedOut)
			}
		})
	}
}

func TestAuthenticate_RevokeFailureStillClearsCookie(t *testing.T) {
	sessions := &fakeSessions{
		sess:      session.Session{UserID: 7},
		logoutErr: errors.New("db down"),
	}
	users := &fakeUsers{user: auth.StoredUser{ID: 7, IsActive: false}}

	res := run(t, sessions, users, true)

	if !res.nextCalled || res.hasUser {
		t.Errorf("nextCalled = %v, hasUser = %v; want true, false", res.nextCalled, res.hasUser)
	}
	if !clearsCookie(res.rec) {
		t.Error("cookie was not cleared")
	}
}

func TestAuthenticate_InfrastructureErrors(t *testing.T) {
	tests := []struct {
		name     string
		sessions *fakeSessions
		users    *fakeUsers
	}{
		{
			"session store failure",
			&fakeSessions{validateErr: errors.New("db down")},
			&fakeUsers{user: activeUser()},
		},
		{
			"user store failure",
			&fakeSessions{sess: session.Session{UserID: 7}},
			&fakeUsers{err: errors.New("db down")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, tt.sessions, tt.users, true)

			if res.nextCalled {
				t.Error("next handler called on infrastructure error")
			}
			if res.rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", res.rec.Code, http.StatusInternalServerError)
			}
			if clearsCookie(res.rec) {
				t.Error("cookie cleared on infrastructure error; user would be logged out by an outage")
			}
			if strings.Contains(res.rec.Body.String(), "db down") {
				t.Error("internal error detail leaked in response body")
			}
		})
	}
}
