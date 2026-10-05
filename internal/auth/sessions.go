package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrInvalidSession = errors.New("invalid or expired session")

// Sessions stores login sessions in Redis.
//
//	session:<sha256(token)>  -> {"user_id":..,"created_at":..}  (expires after ttl)
//	user_sessions:<user id>  -> set of session key hashes, so all of a user's
//	                            sessions can be revoked at once
//
// Only a hash of the token is stored, so a Redis dump does not hand out
// usable sessions.
type Sessions struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewSessions(rdb *redis.Client, ttl time.Duration) *Sessions {
	return &Sessions{rdb: rdb, ttl: ttl}
}

func (s *Sessions) TTL() time.Duration { return s.ttl }

func (s *Sessions) Ping(ctx context.Context) error { return s.rdb.Ping(ctx).Err() }

type sessionData struct {
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sessionKey(hash string) string  { return "session:" + hash }
func userSetKey(userID int64) string { return "user_sessions:" + strconv.FormatInt(userID, 10) }

// Create starts a session for the user and returns its token and expiry.
func (s *Sessions) Create(ctx context.Context, userID int64) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	hash := hashToken(token)
	now := time.Now().UTC()
	data, err := json.Marshal(sessionData{UserID: userID, CreatedAt: now})
	if err != nil {
		return "", time.Time{}, err
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, sessionKey(hash), data, s.ttl)
		p.SAdd(ctx, userSetKey(userID), hash)
		// The index lives as long as the newest session.
		p.Expire(ctx, userSetKey(userID), s.ttl)
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, now.Add(s.ttl), nil
}

// Lookup returns the user ID of a live session.
func (s *Sessions) Lookup(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, ErrInvalidSession
	}
	raw, err := s.rdb.Get(ctx, sessionKey(hashToken(token))).Bytes()
	if errors.Is(err, redis.Nil) {
		return 0, ErrInvalidSession
	}
	if err != nil {
		return 0, err
	}
	var d sessionData
	if err := json.Unmarshal(raw, &d); err != nil {
		return 0, ErrInvalidSession
	}
	return d.UserID, nil
}

// Delete ends one session. Unknown tokens are ignored.
func (s *Sessions) Delete(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := hashToken(token)
	userID, err := s.Lookup(ctx, token)
	if errors.Is(err, ErrInvalidSession) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, sessionKey(hash))
		p.SRem(ctx, userSetKey(userID), hash)
		return nil
	})
	return err
}

// DeleteAll ends every session of the user, e.g. after a password change or
// when the user is deleted.
func (s *Sessions) DeleteAll(ctx context.Context, userID int64) error {
	hashes, err := s.rdb.SMembers(ctx, userSetKey(userID)).Result()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(hashes)+1)
	for _, h := range hashes {
		keys = append(keys, sessionKey(h))
	}
	keys = append(keys, userSetKey(userID))
	return s.rdb.Del(ctx, keys...).Err()
}

func totpSetupKey(userID int64) string { return "totp_setup:" + strconv.FormatInt(userID, 10) }

// TOTPSetupTTL is how long a user has to confirm a new authenticator.
const TOTPSetupTTL = 10 * time.Minute

// SetPendingTOTP holds a secret the user is enrolling until they confirm it
// with a code from their app.
func (s *Sessions) SetPendingTOTP(ctx context.Context, userID int64, secret string) error {
	return s.rdb.Set(ctx, totpSetupKey(userID), secret, TOTPSetupTTL).Err()
}

// PendingTOTP returns the secret being enrolled, or "" if there is none.
func (s *Sessions) PendingTOTP(ctx context.Context, userID int64) (string, error) {
	v, err := s.rdb.Get(ctx, totpSetupKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (s *Sessions) ClearPendingTOTP(ctx context.Context, userID int64) error {
	return s.rdb.Del(ctx, totpSetupKey(userID)).Err()
}
