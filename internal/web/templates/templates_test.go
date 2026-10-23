package templates_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

func TestNew_ParsesEveryPage(t *testing.T) {
	if _, err := templates.New(); err != nil {
		t.Fatal(err)
	}
}

func TestPage_UsesContextForUserAndToken(t *testing.T) {
	set, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := access.ContextWithUser(req.Context(), access.User{ID: 1, Username: "alice", IsPlatformAdmin: true})
	ctx = csrf.ContextWithToken(ctx, "tok-123")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	set.Page(rec, req, http.StatusOK, "home", "Home", nil)

	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"alice", `value="tok-123"`, `"X-CSRF-Token": "tok-123"`, "<title>Home"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPage_TitleIsEscaped(t *testing.T) {
	set, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	set.Page(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, "home", `</title><script>alert(1)</script>`, nil)

	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Error("title injected markup")
	}
}

func TestPage_UnknownPageIs500(t *testing.T) {
	set, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	set.Page(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, "nope", "x", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestFragment_BrokenDataGivesCleanErrorNotAHalfPage(t *testing.T) {
	set, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	set.Fragment(rec, http.StatusOK, "admin/users", "users", struct{}{})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the template cannot render", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<div") {
		t.Error("a partially rendered fragment was sent to the client")
	}
}

func TestLevelLabel(t *testing.T) {
	for level, want := range map[access.Level]string{
		access.LevelView: "View Only",
		access.LevelEdit: "View + Edit",
		access.LevelNone: "No access",
	} {
		if got := templates.LevelLabel(level); got != want {
			t.Errorf("LevelLabel(%v) = %q, want %q", level, got, want)
		}
	}
}
