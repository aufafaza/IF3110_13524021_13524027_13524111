package csrf_test

import (
	"encoding/base64"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

func TestTokenFromSecret(t *testing.T) {
	secret := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	a := csrf.TokenFromSecret(secret)
	b := csrf.TokenFromSecret(secret)
	if a == "" || a != b {
		t.Fatalf("token not deterministic or empty: %q vs %q", a, b)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		t.Fatalf("token is not base64url: %v", err)
	}
	if string(decoded) != string(secret) {
		t.Error("token does not decode back to the secret")
	}

	other := csrf.TokenFromSecret([]byte{9, 9, 9})
	if other == a {
		t.Error("different secrets produced the same token")
	}
}

func TestTokenFromSecret_Empty(t *testing.T) {
	if got := csrf.TokenFromSecret(nil); got != "" {
		t.Errorf("TokenFromSecret(nil) = %q, want empty", got)
	}
	if got := csrf.TokenFromSecret([]byte{}); got != "" {
		t.Errorf("TokenFromSecret(empty) = %q, want empty", got)
	}
}

func TestNewPreSessionToken(t *testing.T) {
	a, err := csrf.NewPreSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := csrf.NewPreSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two tokens were identical")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		t.Fatalf("token is not base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("token decodes to %d bytes, want 32", len(decoded))
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"same", "abc", "abc", true},
		{"different", "abc", "abd", false},
		{"different length", "abc", "abcd", false},
		{"both empty", "", "", false},
		{"first empty", "", "abc", false},
		{"second empty", "abc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := csrf.Equal(tt.a, tt.b); got != tt.want {
				t.Errorf("Equal(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
