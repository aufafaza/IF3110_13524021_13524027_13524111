//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/middleware"
)

func TestHealthOverHTTP(t *testing.T) {
	cookie := session.CookieConfig{Name: "session"}
	srv := httptest.NewServer(web.New(web.Deps{
		Auth:         handlers.NewAuthHandler(nil, nil, cookie),
		Authenticate: middleware.NewAuthenticator(nil, nil, cookie),
		Guard:        access.NewGuard(nil, "/login"),
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
}
