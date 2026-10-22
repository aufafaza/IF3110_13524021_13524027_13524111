package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
)

var testParams = &auth.Params{
	Memory:      8,
	Iterations:  1,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func TestHashThenVerify(t *testing.T) {
	h, err := auth.HashPassword("secret", testParams)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := auth.VerifyPassword("secret", h)
	if err != nil || !ok {
		t.Fatalf("want match, got ok=%v, err=%v", ok, err)
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	h, err := auth.HashPassword("secret", testParams)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := auth.VerifyPassword("not-secret", h)
	if err != nil {
		t.Fatalf("wrong password must not be an error, got %v", err)
	}
	if ok {
		t.Fatal("wrong password verified")
	}
}

func TestHashIsSalted(t *testing.T) {
	h1, _ := auth.HashPassword("secret", testParams)
	h2, _ := auth.HashPassword("secret", testParams)
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical; salt is not random")
	}
}

func TestHashFormat(t *testing.T) {
	h, err := auth.HashPassword("secret", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("unexpected format: %s", h)
	}
}

func TestHashNilParamsUsesDefaults(t *testing.T) {
	h, err := auth.HashPassword("secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "m=65536") {
		t.Fatalf("defaults not applied: %s", h)
	}
}

func TestHashRejectsInvalidParams(t *testing.T) {
	bad := *testParams
	bad.Iterations = 0
	if _, err := auth.HashPassword("secret", &bad); err == nil {
		t.Fatal("want error for zero iterations")
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	valid, err := auth.HashPassword("secret", testParams)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, "$")

	cases := map[string]string{
		"empty":             "",
		"garbage":           "garbage",
		"too few parts":     "$argon2id$v=19$m=8,t=1,p=1$salt",
		"unknown algorithm": strings.Replace(valid, "argon2id", "argon2i", 1),
		"zero iterations":   "$argon2id$v=19$m=8,t=0,p=1$" + parts[4] + "$" + parts[5],
		"zero parallelism":  "$argon2id$v=19$m=8,t=1,p=0$" + parts[4] + "$" + parts[5],
		"trailing junk":     "$argon2id$v=19$m=8,t=1,p=1junk$" + parts[4] + "$" + parts[5],
		"bad base64 salt":   "$argon2id$v=19$m=8,t=1,p=1$!!!$" + parts[5],
		"bad base64 hash":   "$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$!!!",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := auth.VerifyPassword("secret", encoded)
			if ok {
				t.Fatal("malformed hash verified")
			}
			if err == nil {
				t.Fatal("want error for malformed hash")
			}
		})
	}
}

func TestVerifyIncompatibleVersion(t *testing.T) {
	valid, _ := auth.HashPassword("secret", testParams)
	old := strings.Replace(valid, "v=19", "v=16", 1)

	_, err := auth.VerifyPassword("secret", old)
	if !errors.Is(err, auth.ErrIncompatibleVersion) {
		t.Fatalf("want ErrIncompatibleVersion, got %v", err)
	}
}

func TestArgon2idHasherSatisfiesInterface(t *testing.T) {
	var h auth.PasswordHasher = auth.Argon2idHasher{Argon2Params: *testParams}

	enc, err := h.Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := h.Verify("secret", enc)
	if err != nil || !ok {
		t.Fatalf("want match, got ok=%v, err=%v", ok, err)
	}
}
