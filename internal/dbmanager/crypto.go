package dbmanager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Cipher struct{ aead cipher.AEAD }

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead}, nil
}

func (c *Cipher) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

func (c *Cipher) Decrypt(data []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("dbmanager: ciphertext too short")
	}
	return c.aead.Open(nil, data[:n], data[n:], nil)
}