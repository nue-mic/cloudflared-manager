package cfaccount

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// encPrefix marks a value as AES-GCM ciphertext. Values without it are treated
// as plaintext on decrypt (forward-compatibility for imported/legacy data).
const encPrefix = "enc:v1:"

// secretBox performs authenticated symmetric encryption of credential strings
// using AES-256-GCM with a per-value random nonce.
type secretBox struct {
	aead cipher.AEAD
}

// loadOrCreateKey returns the 32-byte data-encryption key stored at path,
// creating it (0600) with cryptographically-random bytes on first use.
func loadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) < 32 {
			return nil, fmt.Errorf("secret key at %s is too short (%d bytes)", path, len(b))
		}
		// Tighten over-permissive perms left by a manual copy / restore. The
		// key must stay 0600 (owner-only); best-effort chmod, ignored on
		// filesystems (e.g. Windows) where POSIX mode bits are nominal.
		if info, serr := os.Stat(path); serr == nil && info.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(path, 0o600)
		}
		return b[:32], nil
	case errors.Is(err, os.ErrNotExist):
		key := make([]byte, 32)
		if _, rerr := rand.Read(key); rerr != nil {
			return nil, fmt.Errorf("generate secret key: %w", rerr)
		}
		if mkerr := os.MkdirAll(filepath.Dir(path), 0o700); mkerr != nil {
			return nil, mkerr
		}
		if werr := os.WriteFile(path, key, 0o600); werr != nil {
			return nil, fmt.Errorf("write secret key: %w", werr)
		}
		return key, nil
	default:
		return nil, err
	}
}

// newSecretBox builds an AES-256-GCM box from a 32-byte key.
func newSecretBox(key []byte) (*secretBox, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretBox{aead: aead}, nil
}

// encrypt returns an enc:v1: token for plain. Empty input stays empty.
func (b *secretBox) encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// decrypt reverses encrypt. A value lacking encPrefix is returned verbatim so
// plaintext-imported secrets keep working.
func (b *secretBox) decrypt(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, encPrefix) {
		return s, nil // plaintext passthrough
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// isEncrypted reports whether s is an enc:v1: token.
func isEncrypted(s string) bool { return strings.HasPrefix(s, encPrefix) }
