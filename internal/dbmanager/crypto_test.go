package dbmanager

import (
	"bytes"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCipherRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	enc, err := c.Encrypt([]byte("rahasia"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("rahasia")) {
		t.Fatal("plaintext terlihat di ciphertext")
	}
	got, err := c.Decrypt(enc)
	if err != nil || string(got) != "rahasia" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestCipherNonceUnique(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt([]byte("x"))
	b, _ := c.Encrypt([]byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("dua enkripsi plaintext sama menghasilkan output identik")
	}
}

func TestCipherRejectsTamperedAndShort(t *testing.T) {
	c := newTestCipher(t)
	enc, _ := c.Encrypt([]byte("x"))
	enc[len(enc)-1] ^= 0xff
	if _, err := c.Decrypt(enc); err == nil {
		t.Fatal("data yang diubah seharusnya ditolak")
	}
	if _, err := c.Decrypt([]byte{1, 2}); err == nil {
		t.Fatal("ciphertext terlalu pendek seharusnya ditolak")
	}
}