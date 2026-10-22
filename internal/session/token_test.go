package session_test

import (
        "bytes"
        "errors"
        "strings"
        "testing"

        "github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
)

func TestNewToken(t *testing.T) {

        raw, hash, err := session.NewToken()
        if err != nil {
                t.Fatal(err)
        }
        if raw == "" {
                t.Error("raw token is empty")
        }
        if len(hash) != 32 {
                t.Errorf("hash length = %d, want 32", len(hash))
        }
        if strings.ContainsAny(raw, "+/=") {
                t.Errorf("raw token is not URL-safe: %q", raw)
        }
}

func TestNewTokenIsUnique(t *testing.T) {
        raw1, hash1, _ := session.NewToken()
        raw2, hash2, _ := session.NewToken()

        if raw1 == raw2 {
                t.Error("two tokens are identical")
        }
        if bytes.Equal(hash1, hash2) {
                t.Error("two hashes are identical")
        }
}

func TestHashTokenMatchesNewToken(t *testing.T) {
        raw, hash, err := session.NewToken()
        if err != nil {
                t.Fatal(err)
        }

        got, err := session.HashToken(raw)
        if err != nil {
                t.Fatal(err)
        }
        if !bytes.Equal(got, hash) {
                t.Fatal("HashToken(raw) does not match the hash NewToken returned")
        }
}

func TestHashTokenIsDeterministic(t *testing.T) {
        raw, _, _ := session.NewToken()

        a, _ := session.HashToken(raw)
        b, _ := session.HashToken(raw)
        if !bytes.Equal(a, b) {
                t.Fatal("same token hashed to different values")
        }
}


func TestHashTokenRejectsInvalid(t *testing.T) {
        tests := map[string]string{
                "empty":        "",
                "not base64":   "!!!not-base64!!!",
                "too short":    "QUJD",
                "too long":     strings.Repeat("A", 100),
                "with padding": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
        }
        for name, raw := range tests {
                t.Run(name, func(t *testing.T) {
                        hash, err := session.HashToken(raw)
                        if !errors.Is(err, session.ErrInvalidToken) {
                                t.Fatalf("want ErrInvalidToken, got %v", err)
                        }
                        if hash != nil {
                                t.Errorf("want nil hash on error, got %x", hash)
                        }
                })
        }
}
func TestNewCSRFSecret(t *testing.T) {
        a, err := session.NewCSRFSecret()
        if err != nil {
                t.Fatal(err)
        }
        b, _ := session.NewCSRFSecret()

        if len(a) != 32 {
                t.Errorf("length = %d, want 32", len(a))
        }
        if bytes.Equal(a, b) {
                t.Error("two secrets are identical")
        }
}
