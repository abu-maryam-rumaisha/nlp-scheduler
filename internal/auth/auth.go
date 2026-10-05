// Package auth hashes passwords and manages login sessions.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Role names. The roles table describes them; what each may do is decided
// here and in the API's route guards.
const (
	RoleViewer     = "viewer"     // read-only access
	RoleProsecutor = "prosecutor" // full access, including user management

	MinPasswordLength = 8
)

// dummyHash is compared against when a username does not exist, so a login
// takes the same time whether or not the user exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)

func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// CheckPassword reports whether password matches hash. Pass an empty hash for
// an unknown user to keep timing uniform.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
