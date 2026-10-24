package access_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type fakeChecker struct {
	level          access.Level
	err            error
	gotUser, gotDB int64
}

func (f *fakeChecker) IsPlatformAdmin(context.Context, int64) (bool, error) {
	panic("unused")
}

func (f *fakeChecker) EffectiveLevel(_ context.Context, userID, dbID int64) (access.Level, error) {
	f.gotUser, f.gotDB = userID, dbID
	return f.level, f.err
}

type result struct {
	status int
	called bool
	loc    string
}

func run(mw func(http.Handler) http.Handler, user *access.User, pathID string) result {
	var called bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if user != nil {
		req = req.WithContext(access.ContextWithUser(req.Context(), *user))
	}
	if pathID != "" {
		req.SetPathValue(access.DatabaseIDParam, pathID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return result{status: rec.Code, called: called, loc: rec.Header().Get("Location")}
}

var (
	member = &access.User{ID: 7, Username: "member"}
	admin  = &access.User{ID: 1, Username: "admin", IsPlatformAdmin: true}
)

func TestRequireLogin(t *testing.T) {
	g := access.NewGuard(&fakeChecker{}, "/login")

	t.Run("anonymous is redirected", func(t *testing.T) {
		r := run(g.RequireLogin, nil, "")
		if r.status != http.StatusSeeOther || r.loc != "/login" || r.called {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("logged in passes", func(t *testing.T) {
		r := run(g.RequireLogin, member, "")
		if r.status != http.StatusOK || !r.called {
			t.Errorf("got %+v", r)
		}
	})
}

func TestRequireAdmin(t *testing.T) {
	g := access.NewGuard(&fakeChecker{}, "/login")

	tests := []struct {
		name       string
		user       *access.User
		wantStatus int
		wantCalled bool
	}{
		{"anonymous is redirected", nil, http.StatusSeeOther, false},
		{"non-admin is forbidden", member, http.StatusForbidden, false},
		{"admin passes", admin, http.StatusOK, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := run(g.RequireAdmin, tc.user, "")
			if r.status != tc.wantStatus || r.called != tc.wantCalled {
				t.Errorf("got %+v", r)
			}
		})
	}
}

func TestRequireDBAccess(t *testing.T) {
	tests := []struct {
		name       string
		user       *access.User
		min        access.Level
		level      access.Level
		checkerErr error
		pathID     string
		wantStatus int
		wantCalled bool
	}{
		{"anonymous is redirected", nil, access.LevelView, access.LevelEdit, nil, "5", http.StatusSeeOther, false},
		{"no grant hides the database", member, access.LevelView, access.LevelNone, nil, "5", http.StatusNotFound, false},
		{"view-only blocked from edit", member, access.LevelEdit, access.LevelView, nil, "5", http.StatusForbidden, false},
		{"view-only can view", member, access.LevelView, access.LevelView, nil, "5", http.StatusOK, true},
		{"edit can view", member, access.LevelView, access.LevelEdit, nil, "5", http.StatusOK, true},
		{"edit can edit", member, access.LevelEdit, access.LevelEdit, nil, "5", http.StatusOK, true},
		{"admin without a grant is denied", admin, access.LevelView, access.LevelNone, nil, "5", http.StatusNotFound, false},
		{"checker failure is a 500", member, access.LevelView, access.LevelNone, errors.New("db down"), "5", http.StatusInternalServerError, false},
		{"non-numeric id is 404", member, access.LevelView, access.LevelEdit, nil, "abc", http.StatusNotFound, false},
		{"missing id is 404", member, access.LevelView, access.LevelEdit, nil, "", http.StatusNotFound, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeChecker{level: tc.level, err: tc.checkerErr}
			g := access.NewGuard(checker, "/login")

			r := run(g.RequireDBAccess(tc.min), tc.user, tc.pathID)
			if r.status != tc.wantStatus || r.called != tc.wantCalled {
				t.Errorf("got %+v, want status=%d called=%v", r, tc.wantStatus, tc.wantCalled)
			}
		})
	}
}

func TestRequireDBAccessPassesUserAndDatabaseToChecker(t *testing.T) {
	checker := &fakeChecker{level: access.LevelEdit}
	g := access.NewGuard(checker, "/login")

	run(g.RequireDBAccess(access.LevelView), member, "42")

	if checker.gotUser != 7 || checker.gotDB != 42 {
		t.Errorf("checker saw user=%d db=%d, want 7 and 42", checker.gotUser, checker.gotDB)
	}
}

func TestRequireDBAccessDoesNotCallCheckerWhenAnonymous(t *testing.T) {
	checker := &fakeChecker{level: access.LevelEdit}
	g := access.NewGuard(checker, "/login")

	run(g.RequireDBAccess(access.LevelView), nil, "42")

	if checker.gotDB != 0 {
		t.Error("checker must not be consulted for an anonymous request")
	}
}

func TestGuard_HTMXRequestsGetAnHXRedirectInsteadOfAHTMLRedirect(t *testing.T) {
	g := access.NewGuard(&fakeChecker{}, "/login")
	guards := map[string]func(http.Handler) http.Handler{
		"RequireLogin":    g.RequireLogin,
		"RequireAdmin":    g.RequireAdmin,
		"RequireDBAccess": g.RequireDBAccess(access.LevelView),
	}
	for name, mw := range guards {
		t.Run(name, func(t *testing.T) {
			called := false
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("HX-Request", "true")
			req.SetPathValue(access.DatabaseIDParam, "1")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if called {
				t.Error("next handler ran for an anonymous request")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("HX-Redirect"); got != "/login" {
				t.Errorf("HX-Redirect = %q, want /login", got)
			}
			if rec.Header().Get("Location") != "" {
				t.Error("an htmx request must not get a Location redirect; the login page would be swapped into a fragment")
			}
		})
	}
}
