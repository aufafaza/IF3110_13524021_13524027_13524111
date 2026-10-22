package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

/*
notes for me:
the idea of tokens are as follows:
1. when login, generate a random token T, hash T to H, store H in db
2. when receiving a req, we will get T, compute H' from T
3. compare H' with H
*/
const tokenBytes = 32

var ErrInvalidToken = errors.New("session: invalid token")

func hashBytes(b []byte) (hash []byte) {
	sum := sha256.Sum256(b)
	hash = sum[:]
	return hash
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("session: random: %w", err)
	}
	return b, nil
}

func NewToken() (raw string, hash []byte, err error) {
	b, err := randomBytes(tokenBytes)
	if err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)

	hash = hashBytes(b)

	return raw, hash, nil
}

func HashToken(raw string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if len(decoded) != tokenBytes {
		return nil, ErrInvalidToken
	}
	return hashBytes(decoded), nil
}

func NewCSRFSecret() ([]byte, error) {
	b, err := randomBytes(tokenBytes)
	if err != nil {
		return nil, err
	}

	return b, nil
}
