package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

type templateRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Variables   []nginx.Variable  `json:"variables"`
	Directives  []nginx.Directive `json:"directives"`
}

func (req *templateRequest) toTemplate() (*store.Template, error) {
	if !nginx.ResourceNameRe.MatchString(req.Name) {
		return nil, &nginx.ValidationError{Path: "name", Msg: "must match " + nginx.ResourceNameRe.String()}
	}
	if err := nginx.ValidateVariables(req.Variables); err != nil {
		return nil, err
	}
	if err := nginx.ValidateTree(req.Directives); err != nil {
		return nil, err
	}
	return &store.Template{
		Name: req.Name, Description: req.Description,
		Variables: req.Variables, Directives: req.Directives,
	}, nil
}

func (s *Server) createTemplate(c fiber.Ctx) error {
	var req templateRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	t, err := req.toTemplate()
	if err != nil {
		return err
	}
	if err := s.store.CreateTemplate(c.Context(), t); err != nil {
		return err
	}
	return s.respondTemplate(c, t.ID, fiber.StatusCreated)
}

func (s *Server) replaceTemplate(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var req templateRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	t, err := req.toTemplate()
	if err != nil {
		return err
	}
	t.ID = id
	if err := s.store.ReplaceTemplate(c.Context(), t); err != nil {
		return err
	}
	return s.respondTemplate(c, id, fiber.StatusOK)
}

func (s *Server) respondTemplate(c fiber.Ctx, id int64, status int) error {
	t, err := s.store.GetTemplate(c.Context(), id)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(t)
}

func (s *Server) listTemplates(c fiber.Ctx) error {
	ts, err := s.store.ListTemplates(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"templates": ts})
}

func (s *Server) getTemplate(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	return s.respondTemplate(c, id, fiber.StatusOK)
}

func (s *Server) deleteTemplate(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := s.store.DeleteTemplate(c.Context(), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// renderTemplate previews the config a deployment would produce.
func (s *Server) renderTemplate(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var req struct {
		Variables map[string]string `json:"variables"`
	}
	if err := decode(c, &req); err != nil {
		return err
	}
	text, vars, err := s.deploy.Render(c.Context(), id, req.Variables)
	if err != nil {
		return err
	}
	if c.Query("format") == "text" {
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
		return c.SendString(text)
	}
	return c.JSON(fiber.Map{"variables": vars, "config": text})
}

type addDirectiveRequest struct {
	nginx.Directive
	// These shadow the embedded fields of the same JSON name.
	ParentID *int64 `json:"parent_id"`
	Position *int   `json:"position"`
}

func (s *Server) addDirective(c fiber.Ctx) error {
	tid, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var req addDirectiveRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	d := req.Directive
	if err := nginx.ValidateDirective(&d); err != nil {
		return err
	}
	if err := s.store.AddDirective(c.Context(), tid, req.ParentID, req.Position, &d); err != nil {
		return err
	}
	return s.respondDirective(c, tid, d.ID, fiber.StatusCreated)
}

func (s *Server) respondDirective(c fiber.Ctx, tid, did int64, status int) error {
	d, err := s.store.GetDirective(c.Context(), tid, did)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(d)
}

func directiveIDs(c fiber.Ctx) (int64, int64, error) {
	tid, err := pathID(c, "id")
	if err != nil {
		return 0, 0, err
	}
	did, err := pathID(c, "did")
	return tid, did, err
}

func (s *Server) getDirective(c fiber.Ctx) error {
	tid, did, err := directiveIDs(c)
	if err != nil {
		return err
	}
	return s.respondDirective(c, tid, did, fiber.StatusOK)
}

func (s *Server) updateDirective(c fiber.Ctx) error {
	tid, did, err := directiveIDs(c)
	if err != nil {
		return err
	}
	var p store.DirectivePatch
	if err := decode(c, &p); err != nil {
		return err
	}
	d, err := s.store.UpdateDirective(c.Context(), tid, did, &p)
	if err != nil {
		return err
	}
	return c.JSON(d)
}

func (s *Server) deleteDirective(c fiber.Ctx) error {
	tid, did, err := directiveIDs(c)
	if err != nil {
		return err
	}
	if err := s.store.DeleteDirective(c.Context(), tid, did); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
