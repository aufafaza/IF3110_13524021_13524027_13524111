package csrf_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

const preCookie = "csrf_login"

func protected() (http.Handler, *bool) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	return csrf.Protect(preCookie)(next), &called
}

func formRequest(method, target string, form url.Values) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func withSessionToken(req *http.Request, token string) *http.Request {
	return req.WithContext(csrf.ContextWithToken(req.Context(), token))
}

func withPreCookie(req *http.Request, value string) *http.Request {
	req.AddCookie(&http.Cookie{Name: preCookie, Value: value})
	return req
}

func TestProtect_SafeMethodsPassWithoutToken(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			h, called := protected()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))

			if rec.Code != http.StatusOK || !*called {
				t.Errorf("status = %d, next called = %v; want 200, true", rec.Code, *called)
			}
		})
	}
}

func TestProtect_UnsafeMethodsAreChecked(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			h, called := protected()
			req := withSessionToken(formRequest(method, "/", url.Values{}), "tok")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden || *called {
				t.Errorf("status = %d, next called = %v; want 403, false", rec.Code, *called)
			}
		})
	}
}

func TestProtect_LoggedIn(t *testing.T) {
	tests := []struct {
		name       string
		req        func() *http.Request
		wantStatus int
	}{
		{
			"correct form token",
			func() *http.Request {
				return withSessionToken(formRequest(http.MethodPost, "/", url.Values{"csrf_token": {"tok"}}), "tok")
			},
			http.StatusOK,
		},
		{
			"correct header token",
			func() *http.Request {
				req := withSessionToken(formRequest(http.MethodPost, "/", url.Values{}), "tok")
				req.Header.Set("X-CSRF-Token", "tok")
				return req
			},
			http.StatusOK,
		},
		{
			"missing token",
			func() *http.Request {
				return withSessionToken(formRequest(http.MethodPost, "/", url.Values{}), "tok")
			},
			http.StatusForbidden,
		},
		{
			"wrong form token",
			func() *http.Request {
				return withSessionToken(formRequest(http.MethodPost, "/", url.Values{"csrf_token": {"nope"}}), "tok")
			},
			http.StatusForbidden,
		},
		{
			"wrong header token",
			func() *http.Request {
				req := withSessionToken(formRequest(http.MethodPost, "/", url.Values{}), "tok")
				req.Header.Set("X-CSRF-Token", "nope")
				return req
			},
			http.StatusForbidden,
		},
		{
			"token only in query string",
			func() *http.Request {
				return withSessionToken(formRequest(http.MethodPost, "/?csrf_token=tok", url.Values{}), "tok")
			},
			http.StatusForbidden,
		},
		{
			"login cookie does not substitute for the session token",
			func() *http.Request {
				req := withSessionToken(formRequest(http.MethodPost, "/", url.Values{"csrf_token": {"pre"}}), "tok")
				return withPreCookie(req, "pre")
			},
			http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, called := protected()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tt.req())

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if wantCalled := tt.wantStatus == http.StatusOK; *called != wantCalled {
				t.Errorf("next called = %v, want %v", *called, wantCalled)
			}
		})
	}
}

func TestProtect_Anonymous(t *testing.T) {
	tests := []struct {
		name       string
		req        func() *http.Request
		wantStatus int
	}{
		{
			"cookie and form match",
			func() *http.Request {
				return withPreCookie(formRequest(http.MethodPost, "/login", url.Values{"csrf_token": {"pre"}}), "pre")
			},
			http.StatusOK,
		},
		{
			"cookie and header match",
			func() *http.Request {
				req := withPreCookie(formRequest(http.MethodPost, "/login", url.Values{}), "pre")
				req.Header.Set("X-CSRF-Token", "pre")
				return req
			},
			http.StatusOK,
		},
		{
			"no cookie",
			func() *http.Request {
				return formRequest(http.MethodPost, "/login", url.Values{"csrf_token": {"pre"}})
			},
			http.StatusForbidden,
		},
		{
			"cookie but no form value",
			func() *http.Request {
				return withPreCookie(formRequest(http.MethodPost, "/login", url.Values{}), "pre")
			},
			http.StatusForbidden,
		},
		{
			"cookie and form differ",
			func() *http.Request {
				return withPreCookie(formRequest(http.MethodPost, "/login", url.Values{"csrf_token": {"other"}}), "pre")
			},
			http.StatusForbidden,
		},
		{
			"empty cookie and empty form",
			func() *http.Request {
				return withPreCookie(formRequest(http.MethodPost, "/login", url.Values{}), "")
			},
			http.StatusForbidden,
		},
		{
			"neither cookie nor form",
			func() *http.Request {
				return formRequest(http.MethodPost, "/login", url.Values{})
			},
			http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, called := protected()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tt.req())

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if wantCalled := tt.wantStatus == http.StatusOK; *called != wantCalled {
				t.Errorf("next called = %v, want %v", *called, wantCalled)
			}
		})
	}
}

func TestProtect_ForbiddenBodyDoesNotRevealCause(t *testing.T) {
	h, _ := protected()

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, withSessionToken(formRequest(http.MethodPost, "/", url.Values{}), "tok"))

	wrong := httptest.NewRecorder()
	h.ServeHTTP(wrong, withSessionToken(formRequest(http.MethodPost, "/", url.Values{"csrf_token": {"nope"}}), "tok"))

	if missing.Body.String() != wrong.Body.String() {
		t.Errorf("bodies differ: %q vs %q", missing.Body.String(), wrong.Body.String())
	}
	if strings.Contains(missing.Body.String(), "tok") {
		t.Error("expected token appears in the response body")
	}
}
