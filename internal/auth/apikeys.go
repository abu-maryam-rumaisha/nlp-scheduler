package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// APIKeyPrefix starts every API key, so keys are recognisable (also by
// secret scanners) and distinguishable from session tokens.
const APIKeyPrefix = "ork_"

// NewAPIKey returns a fresh key, its public prefix and the hash to store.
// The key looks like ork_1a2b3c4d_<43 chars>; the prefix is ork_1a2b3c4d.
func NewAPIKey() (key, prefix string, hash []byte, err error) {
	id := make([]byte, 4)
	secret := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return "", "", nil, err
	}
	if _, err := rand.Read(secret); err != nil {
		return "", "", nil, err
	}
	prefix = APIKeyPrefix + hex.EncodeToString(id)
	key = prefix + "_" + base64.RawURLEncoding.EncodeToString(secret)
	return key, prefix, HashAPIKey(key), nil
}

// IsAPIKey reports whether s has the shape of an API key.
func IsAPIKey(s string) bool { return strings.HasPrefix(s, APIKeyPrefix) }

// APIKeyPrefixOf returns the lookup prefix of a key, or false if the key is
// malformed.
func APIKeyPrefixOf(key string) (string, bool) {
	if !IsAPIKey(key) {
		return "", false
	}
	prefix, secret, ok := strings.Cut(key[len(APIKeyPrefix):], "_")
	if !ok || len(prefix) != 8 || len(secret) != 43 {
		return "", false
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return "", false
	}
	return APIKeyPrefix + prefix, true
}

// HashAPIKey hashes a key for storage. Keys carry 256 random bits, so a fast
// hash is enough; unlike passwords they cannot be guessed.
func HashAPIKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// APIKeyMatches compares a presented key with a stored hash in constant time.
func APIKeyMatches(key string, hash []byte) bool {
	return subtle.ConstantTimeCompare(HashAPIKey(key), hash) == 1
}

var roleRank = map[string]int{RoleViewer: 1, RoleProsecutor: 2}

// RoleAtMost reports whether role grants no more than limit.
func RoleAtMost(role, limit string) bool {
	r, ok := roleRank[role]
	return ok && r <= roleRank[limit]
}

// EffectiveRole is the lesser of a key's role and its owner's current role,
// so demoting a user also limits their keys.
func EffectiveRole(keyRole, ownerRole string) string {
	if roleRank[keyRole] <= roleRank[ownerRole] {
		return keyRole
	}
	return ownerRole
}
