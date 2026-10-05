package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newSessions(t *testing.T) (*Sessions, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	return NewSessions(redis.NewClient(&redis.Options{Addr: mr.Addr()}), time.Hour), mr
}

func TestSessionLifecycle(t *testing.T) {
	s, mr := newSessions(t)
	ctx := context.Background()

	tok, exp, err := s.Create(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) < 59*time.Minute {
		t.Errorf("expiry too soon: %v", exp)
	}
	if id, err := s.Lookup(ctx, tok); err != nil || id != 42 {
		t.Fatalf("lookup: %d %v", id, err)
	}
	// Only the hash of the token is stored.
	for _, k := range mr.Keys() {
		if strings.Contains(k, tok) {
			t.Errorf("raw token stored in key %q", k)
		}
	}
	if _, err := s.Lookup(ctx, tok+"x"); err != ErrInvalidSession {
		t.Errorf("unknown token: %v", err)
	}

	if err := s.Delete(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, tok); err != ErrInvalidSession {
		t.Errorf("deleted session still valid: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s, mr := newSessions(t)
	ctx := context.Background()
	tok, _, _ := s.Create(ctx, 1)
	mr.FastForward(61 * time.Minute)
	if _, err := s.Lookup(ctx, tok); err != ErrInvalidSession {
		t.Errorf("expired session still valid: %v", err)
	}
}

func TestDeleteAll(t *testing.T) {
	s, _ := newSessions(t)
	ctx := context.Background()
	a, _, _ := s.Create(ctx, 7)
	b, _, _ := s.Create(ctx, 7)
	other, _, _ := s.Create(ctx, 8)

	if err := s.DeleteAll(ctx, 7); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a, b} {
		if _, err := s.Lookup(ctx, tok); err != ErrInvalidSession {
			t.Errorf("session survived DeleteAll: %v", err)
		}
	}
	if id, err := s.Lookup(ctx, other); err != nil || id != 8 {
		t.Errorf("another user's session was revoked: %d %v", id, err)
	}
}

func TestPasswords(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong horse") || CheckPassword("", "correct horse") {
		t.Error("CheckPassword gave a wrong answer")
	}
}

func TestAPIKeys(t *testing.T) {
	key, prefix, hash, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, prefix+"_") || len(prefix) != len("ork_")+8 {
		t.Fatalf("bad key %q / prefix %q", key, prefix)
	}
	if p, ok := APIKeyPrefixOf(key); !ok || p != prefix {
		t.Fatalf("prefix of key: %q %v", p, ok)
	}
	if !APIKeyMatches(key, hash) || APIKeyMatches(key+"x", hash) {
		t.Error("APIKeyMatches gave a wrong answer")
	}
	other, _, _, _ := NewAPIKey()
	if other == key {
		t.Error("keys are not random")
	}
	for _, bad := range []string{"", "ork_", "ork_zzzzzzzz_" + strings.Repeat("a", 43), "ork_1234abcd_short", "nope_1234abcd_" + strings.Repeat("a", 43)} {
		if _, ok := APIKeyPrefixOf(bad); ok {
			t.Errorf("malformed key %q accepted", bad)
		}
	}
}

func TestRoles(t *testing.T) {
	if !RoleAtMost(RoleViewer, RoleProsecutor) || !RoleAtMost(RoleViewer, RoleViewer) || RoleAtMost(RoleProsecutor, RoleViewer) {
		t.Error("RoleAtMost")
	}
	if RoleAtMost("admin", RoleProsecutor) {
		t.Error("unknown role accepted")
	}
	if EffectiveRole(RoleProsecutor, RoleViewer) != RoleViewer || EffectiveRole(RoleViewer, RoleProsecutor) != RoleViewer ||
		EffectiveRole(RoleProsecutor, RoleProsecutor) != RoleProsecutor {
		t.Error("EffectiveRole")
	}
}
