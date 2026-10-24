package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const preSessionBytes = 32
const PreSessionCookie = "csrf_login"
func TokenFromSecret(secret []byte) string {
	if len(secret) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(secret)
}

func NewPreSessionToken() (string, error) {
	b := make([]byte, preSessionBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("csrf: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Equal(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
