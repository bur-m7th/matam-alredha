// Package auth provides password hashing and opaque server-side sessions.
//
// Hashing uses PBKDF2-HMAC-SHA256 from the standard library rather than an
// external bcrypt package, so the binary builds with no third-party crypto.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultIterations = 210000
	saltLen           = 16
	keyLen            = 32
)

// pbkdf2 derives a key using HMAC-SHA256 (RFC 8018).
func pbkdf2(password, salt []byte, iter, keyLen int) []byte {
	h := sha256.New
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	buf := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(h, password)
		mac.Write(salt)
		binary.BigEndian.PutUint32(buf, uint32(block))
		mac.Write(buf)
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac := hmac.New(h, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// HashPassword returns an encoded hash of the form pbkdf2_sha256$iter$salt$key.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2([]byte(password), salt, defaultIterations, keyLen)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s",
		defaultIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares a candidate password against an encoded hash in
// constant time.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken returns a 256-bit URL-safe random session token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidatePasswordStrength keeps admin passwords above a usable floor.
func ValidatePasswordStrength(pw string) error {
	if len([]rune(pw)) < 8 {
		return errors.New("كلمة المرور يجب أن تتكون من ثمانية أحرف على الأقل")
	}
	if len(pw) > 200 {
		return errors.New("كلمة المرور طويلة جداً")
	}
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasLetter || !hasDigit {
		return errors.New("كلمة المرور يجب أن تحتوي على أحرف وأرقام")
	}
	return nil
}
