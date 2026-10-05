package api

import (
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

type instanceView struct {
	store.Instance
	Status string `json:"status"` // online, offline or unknown (never seen)
}

func (s *Server) view(in store.Instance) instanceView {
	status := "unknown"
	if in.LastSeenAt != nil {
		status = "offline"
		if time.Since(*in.LastSeenAt) <= s.cfg.OnlineWindow {
			status = "online"
		}
	}
	return instanceView{Instance: in, Status: status}
}

type instanceRequest struct {
	Name        string `json:"name"`
	Service     string `json:"service"`
	Host        string `json:"host"`
	Description string `json:"description"`
}

func (req *instanceRequest) toInstance(id int64) (store.Instance, error) {
	if !nginx.ResourceNameRe.MatchString(req.Name) {
		return store.Instance{}, &nginx.ValidationError{Path: "name", Msg: "must match " + nginx.ResourceNameRe.String()}
	}
	if !nginx.ResourceNameRe.MatchString(req.Service) {
		return store.Instance{}, &nginx.ValidationError{Path: "service", Msg: "must match " + nginx.ResourceNameRe.String()}
	}
	return store.Instance{ID: id, Name: req.Name, Service: req.Service, Host: req.Host, Description: req.Description}, nil
}

func (s *Server) createInstance(c fiber.Ctx) error {
	var req instanceRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	in, err := req.toInstance(0)
	if err != nil {
		return err
	}
	if err := s.store.CreateInstance(c.Context(), &in); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.view(in))
}

func (s *Server) updateInstance(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	var req instanceRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	in, err := req.toInstance(id)
	if err != nil {
		return err
	}
	if err := s.store.UpdateInstance(c.Context(), &in); err != nil {
		return err
	}
	return c.JSON(s.view(in))
}

func (s *Server) listInstances(c fiber.Ctx) error {
	ins, err := s.store.ListInstances(c.Context(), c.Query("service"))
	if err != nil {
		return err
	}
	views := make([]instanceView, len(ins))
	for i, in := range ins {
		views[i] = s.view(in)
	}
	return c.JSON(fiber.Map{"instances": views})
}

func (s *Server) getInstance(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	in, err := s.store.GetInstance(c.Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(s.view(*in))
}

func (s *Server) deleteInstance(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if err := s.store.DeleteInstance(c.Context(), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) listInstanceConfigs(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	if _, err := s.store.GetInstance(c.Context(), id); err != nil {
		return err
	}
	cs, err := s.store.ListInstanceConfigs(c.Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"configs": cs})
}
