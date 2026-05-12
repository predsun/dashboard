// Package auth contains authentication and session primitives.
package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLen is the minimum acceptable password length. Kept short here;
// the setup wizard surfaces a stricter recommendation in the UI.
const MinPasswordLen = 8

var ErrPasswordTooShort = errors.New("password too short")

// HashPassword returns a bcrypt hash suitable for storage.
func HashPassword(plain string) (string, error) {
	if len(plain) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword compares a plaintext password against a stored bcrypt hash.
// Returns nil on match, an error otherwise. Constant-time inside bcrypt.
func VerifyPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
