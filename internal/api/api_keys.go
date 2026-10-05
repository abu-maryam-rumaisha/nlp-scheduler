package api

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/auth"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

const maxAPIKeyTTLDays = 3650

// listAPIKeys returns the caller's keys; prosecutors may pass ?all=true to
// see everyone's.
func (s *Server) listAPIKeys(c fiber.Ctx) error {
	u := currentUser(c)
	userID := u.ID
	if c.Query("all") == "true" {
		if u.Role != auth.RoleProsecutor {
			return httpErr(fiber.StatusForbidden, "only prosecutors can list every user's API keys")
		}
		userID = 0
	}
	keys, err := s.store.ListAPIKeys(c.Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

// createAPIKey creates a key for the caller. The key itself is only ever
// returned here.
func (s *Server) createAPIKey(c fiber.Ctx) error {
	var req struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	u := currentUser(c)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 100 {
		return &httpError{Status: fiber.StatusBadRequest, Field: "name", Msg: "name must be 1-100 characters"}
	}
	if req.Role == "" {
		req.Role = auth.RoleViewer
	}
	if !auth.RoleAtMost(req.Role, u.Role) {
		return &httpError{Status: fiber.StatusBadRequest, Field: "role", Msg: "a key cannot have a higher role than your own (" + u.Role + ")"}
	}
	k := store.APIKey{UserID: u.ID, Name: req.Name, Role: req.Role}
	if req.ExpiresInDays != nil && *req.ExpiresInDays != 0 {
		days := *req.ExpiresInDays
		if days < 1 || days > maxAPIKeyTTLDays {
			return &httpError{Status: fiber.StatusBadRequest, Field: "expires_in_days", Msg: "must be between 1 and 3650, or omitted for no expiry"}
		}
		exp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	key, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		return err
	}
	k.Prefix, k.KeyHash = prefix, hash
	if err := s.store.CreateAPIKey(c.Context(), &k); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"key": key, "api_key": k})
}

// deleteAPIKey revokes a key. Users may revoke their own keys, prosecutors
// anyone's; other keys read as not found.
func (s *Server) deleteAPIKey(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	u := currentUser(c)
	k, err := s.store.GetAPIKey(c.Context(), id)
	if err != nil {
		return err
	}
	if k.UserID != u.ID && u.Role != auth.RoleProsecutor {
		return store.ErrNotFound
	}
	if err := s.store.DeleteAPIKey(c.Context(), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
