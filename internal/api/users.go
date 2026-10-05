package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/auth"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

func (s *Server) listRoles(c fiber.Ctx) error {
	rs, err := s.store.ListRoles(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"roles": rs})
}

func (s *Server) listUsers(c fiber.Ctx) error {
	us, err := s.store.ListUsers(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"users": us})
}

func (s *Server) getUser(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	u, err := s.store.GetUser(c.Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(u)
}

func (s *Server) createUser(c fiber.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	if !nginx.ResourceNameRe.MatchString(req.Username) {
		return &httpError{Status: fiber.StatusBadRequest, Field: "username", Msg: "must match " + nginx.ResourceNameRe.String()}
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return &httpError{Status: fiber.StatusBadRequest, Field: "password", Msg: err.Error()}
	}
	u := store.User{Username: req.Username, Role: req.Role, PasswordHash: hash}
	if err := s.store.CreateUser(c.Context(), &u); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(u)
}

// updateUser changes a user's role and/or resets their password.
func (s *Server) updateUser(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var req struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	if req.Role != nil {
		// Keeps at least one prosecutor: the one making the request.
		if id == currentUser(c).ID && *req.Role != auth.RoleProsecutor {
			return httpErr(fiber.StatusBadRequest, "you cannot remove your own prosecutor role")
		}
	}
	var hash *string
	if req.Password != nil {
		h, err := auth.HashPassword(*req.Password)
		if err != nil {
			return &httpError{Status: fiber.StatusBadRequest, Field: "password", Msg: err.Error()}
		}
		hash = &h
	}
	u, err := s.store.UpdateUser(c.Context(), id, req.Role, hash)
	if err != nil {
		return err
	}
	// A password reset signs the user out everywhere.
	if hash != nil {
		if err := s.sessions.DeleteAll(c.Context(), id); err != nil {
			return err
		}
	}
	return c.JSON(u)
}

func (s *Server) deleteUser(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if id == currentUser(c).ID {
		return httpErr(fiber.StatusBadRequest, "you cannot delete yourself")
	}
	if err := s.store.DeleteUser(c.Context(), id); err != nil {
		return err
	}
	if err := s.sessions.DeleteAll(c.Context(), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
