// Package cryptotest is the conformance suite for a
// [connectionforge.Cryptographer], and an in-memory cryptographer for tests.
package cryptotest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"

	cf "github.com/plexusone/connectionforge"
)

// Run checks that c round-trips, never stores the plaintext, and refuses a
// ciphertext opened with another purpose or other additional data.
func Run(t *testing.T, c cf.Cryptographer) {
	t.Helper()
	ctx := context.Background()
	plaintext := []byte("sentinel-credential-value")
	aad := cf.CredentialAAD("org-a", "c1", 1)

	sealed, err := c.Seal(ctx, cf.CredentialPurpose, plaintext, aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := c.Open(ctx, cf.CredentialPurpose, sealed, aad)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("open: %q, %v", got, err)
	}
	if _, err := c.Open(ctx, cf.CredentialPurpose, sealed, cf.CredentialAAD("org-b", "c1", 1)); err == nil {
		t.Error("opened with another owner's aad")
	}
	if _, err := c.Open(ctx, cf.CredentialPurpose, sealed, cf.CredentialAAD("org-a", "c1", 2)); err == nil {
		t.Error("opened with another version's aad")
	}
	if _, err := c.Open(ctx, "another.purpose", sealed, aad); err == nil {
		t.Error("opened under another purpose")
	}
	again, err := c.Seal(ctx, cf.CredentialPurpose, plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(again, sealed) {
		t.Error("sealing twice gave the same ciphertext")
	}
}

// Cryptographer is an in-memory AES-256-GCM cryptographer for tests. Its key
// is random and lives only in memory.
type Cryptographer struct {
	key []byte
}

// New returns a cryptographer with a random key.
func New() *Cryptographer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return &Cryptographer{key: key}
}

func (c *Cryptographer) aead() cipher.AEAD {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		panic(err) // the key is always 32 bytes
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		panic(err)
	}
	return aead
}

// bind folds the purpose into the additional data.
func bind(purpose string, aad []byte) []byte {
	h := sha256.Sum256([]byte(purpose))
	return append(h[:], aad...)
}

// Seal implements [cf.Cryptographer].
func (c *Cryptographer) Seal(ctx context.Context, purpose string, plaintext, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.aead().Seal(nil, nil, plaintext, bind(purpose, aad)), nil //nolint:gosec // G407: a nil nonce is the API of a random-nonce AEAD, which generates the nonce itself
}

// Open implements [cf.Cryptographer].
func (c *Cryptographer) Open(ctx context.Context, purpose string, ciphertext, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pt, err := c.aead().Open(nil, nil, ciphertext, bind(purpose, aad))
	if err != nil {
		return nil, errors.New("cryptotest: cannot open")
	}
	return pt, nil
}
