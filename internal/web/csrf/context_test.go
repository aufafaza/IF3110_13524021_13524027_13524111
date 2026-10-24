package csrf_test

import (
	"context"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

func TestContextToken(t *testing.T) {
	ctx := context.Background()
	if _, ok := csrf.TokenFromContext(ctx); ok {
		t.Error("token found in an empty context")
	}

	ctx = csrf.ContextWithToken(ctx, "tok")
	got, ok := csrf.TokenFromContext(ctx)
	if !ok || got != "tok" {
		t.Errorf("TokenFromContext = %q, %v; want tok, true", got, ok)
	}
}

func TestContextToken_EmptyIsAbsent(t *testing.T) {
	ctx := csrf.ContextWithToken(context.Background(), "")
	if _, ok := csrf.TokenFromContext(ctx); ok {
		t.Error("empty token reported as present")
	}
}
