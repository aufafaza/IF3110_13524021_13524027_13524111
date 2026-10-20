//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tubes1/internal/server"
)

func TestHealthOverHTTP(t *testing.T) {
	srv := httptest.NewServer(server.New())
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
