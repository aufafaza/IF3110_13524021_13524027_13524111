package session_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

var cookieCfg = session.CookieConfig{
	Name:     "session",
	Secure:   true,
	SameSite: http.SameSiteLaxMode,
}

func setCookieAndRead(t *testing.T, cfg session.CookieConfig, value string, expires time.Time) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	session.SetCookie(rec, cfg, value, expires)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestSetCookieAttributes(t *testing.T) {
	expires := time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC)
	c := setCookieAndRead(t, cookieCfg, "token-value", expires)

	if c.Name != "session" || c.Value != "token-value" {
		t.Errorf("name/value = %q/%q", c.Name, c.Value)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must always be set")
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if !c.Secure {
		t.Error("Secure should follow the config (true)")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if !c.Expires.Equal(expires) {
		t.Errorf("Expires = %v, want %v", c.Expires, expires)
	}
}

func TestSetCookieFollowsConfig(t *testing.T) {
	tests := []struct {
		name     string
		secure   bool
		sameSite http.SameSite
	}{
		{"secure lax", true, http.SameSiteLaxMode},
		{"insecure strict", false, http.SameSiteStrictMode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := session.CookieConfig{Name: "session", Secure: tc.secure, SameSite: tc.sameSite}
			c := setCookieAndRead(t, cfg, "v", time.Now().Add(time.Hour))

			if c.Secure != tc.secure {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.secure)
			}
			if c.SameSite != tc.sameSite {
				t.Errorf("SameSite = %v, want %v", c.SameSite, tc.sameSite)
			}
			if !c.HttpOnly {
				t.Error("HttpOnly must be set regardless of config")
			}
		})
	}
}

func TestClearCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	session.ClearCookie(rec, cookieCfg)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]

	if c.Name != "session" || c.Value != "" {
		t.Errorf("name/value = %q/%q, want session/empty", c.Name, c.Value)
	}
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative so the browser deletes it", c.MaxAge)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, must match the original so the browser deletes it", c.Path)
	}
}

func TestReadCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})

	got, ok := session.ReadCookie(req, cookieCfg)
	if !ok || got != "abc" {
		t.Errorf("ReadCookie = %q, %v; want abc, true", got, ok)
	}
}

func TestReadCookieMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got, ok := session.ReadCookie(req, cookieCfg)
	if ok || got != "" {
		t.Errorf("ReadCookie = %q, %v; want empty, false", got, ok)
	}
}

func TestReadCookieIgnoresOtherNames(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "other", Value: "abc"})

	if _, ok := session.ReadCookie(req, cookieCfg); ok {
		t.Error("a cookie with a different name must not be read")
	}
}

func TestParseSameSite(t *testing.T) {
	tests := map[string]http.SameSite{
		"Lax":     http.SameSiteLaxMode,
		"Strict":  http.SameSiteStrictMode,
		"":        http.SameSiteLaxMode,
		"unknown": http.SameSiteLaxMode,
	}
	for in, want := range tests {
		if got := session.ParseSameSite(in); got != want {
			t.Errorf("ParseSameSite(%q) = %v, want %v", in, got, want)
		}
	}
}
