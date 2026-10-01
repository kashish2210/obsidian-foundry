// Package password hashes and verifies passwords with PBKDF2-HMAC-SHA256
// from the standard library.
package password

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	scheme     = "pbkdf2-sha256"
	iterations = 50000
	keyLen     = 32
	saltLen    = 16
)

var enc = base64.RawStdEncoding

// Hash returns a self-describing salted hash of the password.
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iterations, keyLen)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	return strings.Join([]string{scheme, strconv.Itoa(iterations), enc.EncodeToString(salt), enc.EncodeToString(key)}, "$"), nil
}

// Verify reports whether pw matches an encoded hash produced by Hash.
func Verify(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
