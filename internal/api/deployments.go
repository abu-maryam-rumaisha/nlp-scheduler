package api

import (
	"strconv"

	"github.com/gofiber/fiber/v3"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/deploy"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

func (s *Server) up(c fiber.Ctx) error {
	var req deploy.UpRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	req.RequestedBy = actor(c)
	ds, err := s.deploy.Up(c.Context(), req)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"deployments": ds})
}

func (s *Server) down(c fiber.Ctx) error {
	var req deploy.DownRequest
	if err := decode(c, &req); err != nil {
		return err
	}
	req.RequestedBy = actor(c)
	ds, err := s.deploy.Down(c.Context(), req)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"deployments": ds})
}

func (s *Server) listDeployments(c fiber.Ctx) error {
	f := store.DeploymentFilter{ConfigName: c.Query("config_name"), Status: c.Query("status")}
	f.InstanceID, _ = strconv.ParseInt(c.Query("instance_id"), 10, 64)
	f.Limit, _ = strconv.Atoi(c.Query("limit"))
	f.Offset, _ = strconv.Atoi(c.Query("offset"))
	ds, total, err := s.store.ListDeployments(c.Context(), f)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deployments": ds, "total": total})
}

func (s *Server) getDeployment(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	d, err := s.store.GetDeployment(c.Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(d)
}

func (s *Server) removeDeployment(c fiber.Ctx) error {
	id, err := pathID(c, "id")
	if err != nil {
		return err
	}
	d, err := s.deploy.Remove(c.Context(), id, actor(c))
	if err != nil {
		return err
	}
	if d == nil {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"deployment": d})
}
