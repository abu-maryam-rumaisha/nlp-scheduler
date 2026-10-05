package api

import (
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

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
		// Code is the authenticator app code, needed when the user has
		// two-factor authentication on.
		Code string `json:"code"`
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
	if u.TOTPEnabled {
		// The client recognises field "code" as "ask for the code and retry".
		if req.Code == "" {
			return &httpError{Status: fiber.StatusUnauthorized, Msg: "enter the code from your authenticator app", Field: "code"}
		}
		if err := s.checkTOTP(c, u.ID, u.TOTPSecret, req.Code, fiber.StatusUnauthorized); err != nil {
			return err
		}
	}
	return s.startSession(c, u)
}

// checkTOTP verifies an authenticator code and marks it used. A wrong or
// reused code fails with the given status and field "code".
func (s *Server) checkTOTP(c fiber.Ctx, userID int64, secret, code string, status int) error {
	invalid := &httpError{Status: status, Msg: "invalid or already used authenticator code", Field: "code"}
	step, ok := auth.CheckTOTP(secret, code, time.Now())
	if !ok {
		return invalid
	}
	fresh, err := s.store.UseTOTPStep(c.Context(), userID, step)
	if err != nil {
		return err
	}
	if !fresh {
		return invalid
	}
	return nil
}

// setupTOTP starts enrolling an authenticator app: it creates a secret and
// returns it with a QR code to scan. Nothing changes until enableTOTP
// confirms a code.
func (s *Server) setupTOTP(c fiber.Ctx) error {
	u := currentUser(c)
	if u.TOTPEnabled {
		return httpErr(fiber.StatusConflict, "two-factor authentication is already on")
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		return err
	}
	if err := s.sessions.SetPendingTOTP(c.Context(), u.ID, secret); err != nil {
		return err
	}
	otpURL := auth.TOTPURL(secret, u.Username)
	qrCode, err := auth.TOTPQRCode(otpURL)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"secret":      secret,
		"otpauth_url": otpURL,
		"qr_code":     qrCode,
		"expires_at":  time.Now().Add(auth.TOTPSetupTTL).UTC(),
	})
}

// enableTOTP turns two-factor authentication on once the user proves their
// app works. Other sessions are ended and this client gets a fresh one.
func (s *Server) enableTOTP(c fiber.Ctx) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	if u.TOTPEnabled {
		return httpErr(fiber.StatusConflict, "two-factor authentication is already on")
	}
	secret, err := s.sessions.PendingTOTP(c.Context(), u.ID)
	if err != nil {
		return err
	}
	if secret == "" {
		return httpErr(fiber.StatusBadRequest, "setup expired, start again")
	}
	step, ok := auth.CheckTOTP(secret, req.Code, time.Now())
	if !ok {
		return &httpError{Status: fiber.StatusBadRequest, Msg: "invalid authenticator code", Field: "code"}
	}
	if u, err = s.store.SetTOTPSecret(c.Context(), u.ID, secret); err != nil {
		return err
	}
	if _, err := s.store.UseTOTPStep(c.Context(), u.ID, step); err != nil {
		return err
	}
	if err := s.sessions.ClearPendingTOTP(c.Context(), u.ID); err != nil {
		return err
	}
	if err := s.sessions.DeleteAll(c.Context(), u.ID); err != nil {
		return err
	}
	return s.startSession(c, u)
}

// disableTOTP turns the user's own two-factor authentication off. It needs
// both the password and a current code.
func (s *Server) disableTOTP(c fiber.Ctx) error {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	if !u.TOTPEnabled {
		return httpErr(fiber.StatusConflict, "two-factor authentication is not on")
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		return &httpError{Status: fiber.StatusBadRequest, Msg: "password is incorrect", Field: "password"}
	}
	if err := s.checkTOTP(c, u.ID, u.TOTPSecret, req.Code, fiber.StatusBadRequest); err != nil {
		return err
	}
	u, err := s.store.SetTOTPSecret(c.Context(), u.ID, "")
	if err != nil {
		return err
	}
	return c.JSON(u)
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

// updateProfile lets any user set their own display name and email. Empty
// values clear them.
func (s *Server) updateProfile(c fiber.Ctx) error {
	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.DisplayName)
	if utf8.RuneCountInString(name) > 100 {
		return &httpError{Status: fiber.StatusBadRequest, Msg: "must be at most 100 characters", Field: "display_name"}
	}
	email := strings.TrimSpace(req.Email)
	if email != "" {
		// Only a bare address, not "Name <addr>".
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
			return &httpError{Status: fiber.StatusBadRequest, Msg: "not a valid email address", Field: "email"}
		}
	}
	u, err := s.store.UpdateProfile(c.Context(), currentUser(c).ID, name, email)
	if err != nil {
		return err
	}
	return c.JSON(u)
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
