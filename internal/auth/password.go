package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Params struct {
	Memory      uint32 
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultParams = Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

var (
	ErrInvalidHash         = errors.New("incorrect hash format")
	ErrIncompatibleVersion = errors.New("incompatible version of argon2")
)

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

type Argon2idHasher struct {
	Argon2Params Params
}

func NewArgon2idHasher() Argon2idHasher {
	return Argon2idHasher{Argon2Params: DefaultParams}
}

func (h Argon2idHasher) Hash(password string) (string, error) {
	return HashPassword(password, &h.Argon2Params)
}

func (h Argon2idHasher) Verify(password, encodedHash string) (bool, error) {
	return VerifyPassword(password, encodedHash)
}

func generateRandomBytes(n uint32) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func (p *Params) validate() error {
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 ||
		p.SaltLength == 0 || p.KeyLength == 0 {
		return ErrInvalidHash
	}
	return nil
}

func HashPassword(password string, p *Params) (encodedHash string, err error) {
	if p == nil {
		p = &DefaultParams
	}
	if err := p.validate(); err != nil {
		return "", err
	}

	salt, err := generateRandomBytes(p.SaltLength)
	if err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encodedHash = fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism, b64Salt, b64Hash)
	return encodedHash, nil
}

func VerifyPassword(password, encodedHash string) (match bool, err error) {
	p, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	otherHash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	return subtle.ConstantTimeCompare(hash, otherHash) == 1, nil
}

func decodeHash(encodedHash string) (p *Params, salt, hash []byte, err error) {
	vals := strings.Split(encodedHash, "$")
	if len(vals) != 6 || vals[0] != "" || vals[1] != "argon2id" {
		return nil, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err = fmt.Sscanf(vals[2], "v=%d", &version); err != nil {
		return nil, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return nil, nil, nil, ErrIncompatibleVersion
	}

	p = &Params{}
	var tail string
	n, _ := fmt.Sscanf(vals[3]+" ", "m=%d,t=%d,p=%d%s", &p.Memory, &p.Iterations, &p.Parallelism, &tail)
	if n != 3 {
		return nil, nil, nil, ErrInvalidHash
	}

	salt, err = base64.RawStdEncoding.Strict().DecodeString(vals[4])
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}
	p.SaltLength = uint32(len(salt))

	hash, err = base64.RawStdEncoding.Strict().DecodeString(vals[5])
	if err != nil {
		return nil, nil, nil, ErrInvalidHash
	}
	p.KeyLength = uint32(len(hash))

	if err := p.validate(); err != nil {
		return nil, nil, nil, err
	}
	return p, salt, hash, nil
}
