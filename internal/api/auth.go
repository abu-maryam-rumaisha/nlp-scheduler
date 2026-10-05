package api

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/auth"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

const (
	// The __Host- prefix makes browsers insist on Secure, Path=/ and no
	// Domain, so the cookie cannot be set or overridden by a subdomain.
	sessionCookie = "__Host-session"
	userKey       = "user"
	tokenKey      = "session-token"
	apiKeyKey     = "api-key"
	apiKeyHeader  = "X-API-Key"
)

// currentUser returns the user set by authenticate.
func currentUser(c fiber.Ctx) *store.User {
	u, _ := c.Locals(userKey).(*store.User)
	return u
}

// currentAPIKey returns the API key the request authenticated with, if any.
func currentAPIKey(c fiber.Ctx) *store.APIKey {
	k, _ := c.Locals(apiKeyKey).(*store.APIKey)
	return k
}

// actor names who made the request, for audit fields such as requested_by.
func actor(c fiber.Ctx) string {
	name := currentUser(c).Username
	if k := currentAPIKey(c); k != nil {
		name += " (API key: " + k.Name + ")"
	}
	return name
}

func bearerToken(c fiber.Ctx) string {
	if h := c.Get(fiber.HeaderAuthorization); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// sessionToken reads the bearer token (API clients) or the session cookie
// (web UI).
func sessionToken(c fiber.Ctx) string {
	if t := bearerToken(c); t != "" && !auth.IsAPIKey(t) {
		return t
	}
	return c.Cookies(sessionCookie)
}

// apiKeyFromRequest reads an API key from X-API-Key or an ork_ bearer token.
func apiKeyFromRequest(c fiber.Ctx) string {
	if k := c.Get(apiKeyHeader); k != "" {
		return k
	}
	if t := bearerToken(c); auth.IsAPIKey(t) {
		return t
	}
	return ""
}

// authenticate accepts an API key or a session (from Redis), and loads the
// user from the database, so role changes and deletions take effect
// immediately.
func (s *Server) authenticate(c fiber.Ctx) error {
	if key := apiKeyFromRequest(c); key != "" {
		return s.authenticateAPIKey(c, key)
	}
	token := sessionToken(c)
	if token == "" {
		return httpErr(fiber.StatusUnauthorized, "authentication required")
	}
	id, err := s.sessions.Lookup(c.Context(), token)
	if errors.Is(err, auth.ErrInvalidSession) {
		return httpErr(fiber.StatusUnauthorized, err.Error())
	}
	if err != nil {
		return err
	}
	u, err := s.store.GetUser(c.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.sessions.Delete(c.Context(), token)
		return httpErr(fiber.StatusUnauthorized, auth.ErrInvalidSession.Error())
	}
	if err != nil {
		return err
	}
	c.Locals(userKey, u)
	c.Locals(tokenKey, token)
	return c.Next()
}

// authenticateAPIKey acts as the key's owner, limited to the key's role.
func (s *Server) authenticateAPIKey(c fiber.Ctx, key string) error {
	invalid := httpErr(fiber.StatusUnauthorized, "invalid API key")
	prefix, ok := auth.APIKeyPrefixOf(key)
	if !ok {
		return invalid
	}
	k, err := s.store.GetAPIKeyByPrefix(c.Context(), prefix)
	if errors.Is(err, store.ErrNotFound) {
		return invalid
	}
	if err != nil {
		return err
	}
	if !auth.APIKeyMatches(key, k.KeyHash) {
		return invalid
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return httpErr(fiber.StatusUnauthorized, "API key expired")
	}
	owner, err := s.store.GetUser(c.Context(), k.UserID)
	if err != nil {
		return err
	}
	u := *owner
	u.Role = auth.EffectiveRole(k.Role, owner.Role)
	c.Locals(userKey, &u)
	c.Locals(apiKeyKey, k)
	if err := s.store.TouchAPIKey(c.Context(), k.ID); err != nil {
		slog.Warn("recording API key use failed", "key", k.Prefix, "err", err)
	}
	return c.Next()
}

// requireSession rejects requests made with an API key.
func (s *Server) requireSession(c fiber.Ctx) error {
	if currentAPIKey(c) != nil {
		return httpErr(fiber.StatusForbidden, "this action requires signing in; API keys cannot be used")
	}
	return c.Next()
}

func (s *Server) requireRole(role string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if u := currentUser(c); u == nil || u.Role != role {
			return httpErr(fiber.StatusForbidden, "this action requires the "+role+" role")
		}
		return c.Next()
	}
}

func (s *Server) setSessionCookie(c fiber.Ctx, token string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		Secure:   true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

// startSession creates a session and responds with it as cookie and body.
func (s *Server) startSession(c fiber.Ctx, u *store.User) error {
	token, exp, err := s.sessions.Create(c.Context(), u.ID)
	if err != nil {
		return err
	}
	s.setSessionCookie(c, token, exp)
	return c.JSON(fiber.Map{"token": token, "expires_at": exp, "user": u})
}

func (s *Server) login(c fiber.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	u, err := s.store.GetUserByUsername(c.Context(), req.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash := ""
	if u != nil {
		hash = u.PasswordHash
	}
	if !auth.CheckPassword(hash, req.Password) {
		return httpErr(fiber.StatusUnauthorized, "invalid username or password")
	}
	return s.startSession(c, u)
}

// logout ends the current session in Redis and clears the cookie.
func (s *Server) logout(c fiber.Ctx) error {
	if err := s.sessions.Delete(c.Context(), sessionToken(c)); err != nil {
		return err
	}
	s.setSessionCookie(c, "", time.Unix(0, 0))
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) me(c fiber.Ctx) error {
	return c.JSON(currentUser(c))
}

// changePassword lets any user change their own password. All of the user's
// sessions are ended and this client gets a fresh one.
func (s *Server) changePassword(c fiber.Ctx) error {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	if !auth.CheckPassword(u.PasswordHash, req.CurrentPassword) {
		return &httpError{Status: fiber.StatusBadRequest, Msg: "current password is incorrect", Field: "current_password"}
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return &httpError{Status: fiber.StatusBadRequest, Msg: err.Error(), Field: "new_password"}
	}
	u, err = s.store.UpdateUser(c.Context(), u.ID, nil, &hash)
	if err != nil {
		return err
	}
	if err := s.sessions.DeleteAll(c.Context(), u.ID); err != nil {
		return err
	}
	return s.startSession(c, u)
}
