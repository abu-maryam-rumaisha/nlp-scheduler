// Package api exposes templates, instances, deployments and users over HTTP
// and serves the web UI.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/auth"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/deploy"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

type Config struct {
	// An instance counts as online if it sent a heartbeat within this window.
	OnlineWindow time.Duration
}

type Server struct {
	store    *store.Store
	deploy   *deploy.Service
	sessions *auth.Sessions
	cfg      Config
}

func NewServer(st *store.Store, dep *deploy.Service, sessions *auth.Sessions, cfg Config) *Server {
	return &Server{store: st, deploy: dep, sessions: sessions, cfg: cfg}
}

// App builds the Fiber application. ui is the built SPA (index.html and
// assets); it may be nil.
func (s *Server) App(ui fs.FS) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "openresty-manager",
		BodyLimit:    4 << 20,
		ReadTimeout:  30 * time.Second,
		Immutable:    true,
		ErrorHandler: errorHandler,
	})
	app.Use(recover.New())
	app.Use(logRequests)

	app.Get("/healthz", s.healthz)

	v1 := app.Group("/api/v1")

	// Public.
	v1.Post("/auth/login", limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
		LimitReached: func(c fiber.Ctx) error {
			return httpErr(fiber.StatusTooManyRequests, "too many login attempts, try again later")
		},
	}), s.login)
	v1.Post("/auth/logout", s.logout)

	// Everything below requires a session or an API key. Reads are open to
	// every role; writes need the prosecutor role. Account management
	// (passwords, users, API keys) needs a signed-in session, so a leaked
	// key cannot mint more keys or take over accounts.
	v1.Use(s.authenticate)
	write := s.requireRole(auth.RoleProsecutor)
	session := s.requireSession

	v1.Get("/auth/me", s.me)
	v1.Put("/auth/profile", session, s.updateProfile)
	v1.Put("/auth/password", session, s.changePassword)
	v1.Post("/auth/totp/setup", session, s.setupTOTP)
	v1.Post("/auth/totp/enable", session, s.enableTOTP)
	v1.Post("/auth/totp/disable", session, s.disableTOTP)

	v1.Get("/roles", s.listRoles)

	v1.Get("/users", session, write, s.listUsers)
	v1.Post("/users", session, write, s.createUser)
	v1.Get("/users/:id", session, write, s.getUser)
	v1.Patch("/users/:id", session, write, s.updateUser)
	v1.Delete("/users/:id", session, write, s.deleteUser)

	v1.Get("/api-keys", session, s.listAPIKeys)
	v1.Post("/api-keys", session, s.createAPIKey)
	v1.Delete("/api-keys/:id", session, s.deleteAPIKey)

	v1.Get("/templates", s.listTemplates)
	v1.Post("/templates", write, s.createTemplate)
	v1.Get("/templates/:id", s.getTemplate)
	v1.Put("/templates/:id", write, s.replaceTemplate)
	v1.Delete("/templates/:id", write, s.deleteTemplate)
	v1.Post("/templates/:id/render", s.renderTemplate) // read-only preview

	v1.Post("/templates/:id/directives", write, s.addDirective)
	v1.Get("/templates/:id/directives/:did", s.getDirective)
	v1.Patch("/templates/:id/directives/:did", write, s.updateDirective)
	v1.Delete("/templates/:id/directives/:did", write, s.deleteDirective)

	v1.Get("/instances", s.listInstances)
	v1.Post("/instances", write, s.createInstance)
	v1.Get("/instances/:id", s.getInstance)
	v1.Put("/instances/:id", write, s.updateInstance)
	v1.Delete("/instances/:id", write, s.deleteInstance)
	v1.Get("/instances/:id/configs", s.listInstanceConfigs)

	v1.Post("/deployments/up", write, s.up)
	v1.Post("/deployments/down", write, s.down)
	v1.Get("/deployments", s.listDeployments)
	v1.Get("/deployments/:id", s.getDeployment)
	v1.Delete("/deployments/:id", write, s.removeDeployment)

	v1.Use(func(c fiber.Ctx) error { return fiber.ErrNotFound })

	app.Get("/*", spaHandler(ui))
	return app
}

func (s *Server) healthz(c fiber.Ctx) error {
	err := s.store.Ping(c.Context())
	if err == nil {
		err = s.sessions.Ping(c.Context())
	}
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "unhealthy", "error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

// httpError is a client-facing error with a status code.
type httpError struct {
	Status int
	Msg    string
	Field  string
}

func (e *httpError) Error() string { return e.Msg }

func httpErr(status int, msg string) error { return &httpError{Status: status, Msg: msg} }

type errorBody struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

func errorHandler(c fiber.Ctx, err error) error {
	var (
		he *httpError
		ve *nginx.ValidationError
		fe *fiber.Error
	)
	status, body := fiber.StatusInternalServerError, errorBody{Error: "internal error"}
	switch {
	case errors.As(err, &he):
		status, body = he.Status, errorBody{Error: he.Msg, Field: he.Field}
	case errors.As(err, &ve):
		status, body = fiber.StatusBadRequest, errorBody{Error: ve.Msg, Field: ve.Path}
	case errors.Is(err, store.ErrNotFound):
		status, body = fiber.StatusNotFound, errorBody{Error: "not found"}
	case errors.Is(err, store.ErrUnknownRole):
		status, body = fiber.StatusBadRequest, errorBody{Error: "unknown role", Field: "role"}
	case errors.Is(err, store.ErrInProgress):
		status, body = fiber.StatusConflict, errorBody{Error: "the deployment or its removal is still in progress"}
	case errors.Is(err, store.ErrConflict):
		status, body = fiber.StatusConflict, errorBody{Error: "a resource with this name already exists"}
	case errors.As(err, &fe):
		status, body = fe.Code, errorBody{Error: fe.Message}
	default:
		slog.Error("request failed", "method", c.Method(), "path", c.Path(), "err", err)
	}
	return c.Status(status).JSON(body)
}

// decode strictly parses the JSON body into v.
func decode(c fiber.Ctx, v any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return httpErr(fiber.StatusBadRequest, "invalid JSON body: "+err.Error())
	}
	return nil
}

func pathID(c fiber.Ctx, name string) (int64, error) {
	id, err := strconv.ParseInt(c.Params(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpErr(fiber.StatusBadRequest, "invalid "+name)
	}
	return id, nil
}

func logRequests(c fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	status := c.Response().StatusCode()
	if err != nil {
		// The error handler has not run yet; approximate the final status.
		var he *httpError
		var fe *fiber.Error
		switch {
		case errors.As(err, &he):
			status = he.Status
		case errors.As(err, &fe):
			status = fe.Code
		}
	}
	slog.Info("http", "method", c.Method(), "path", c.Path(), "status", status, "duration", time.Since(start))
	return err
}
