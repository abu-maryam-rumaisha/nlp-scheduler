// Package deploy turns up/down requests into deployment rows and agent
// commands, and folds agent results and heartbeats back into the database.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/mq"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
)

type Service struct {
	store    *store.Store
	pub      *mq.Publisher
	exchange string
}

func NewService(st *store.Store, pub *mq.Publisher, exchange string) *Service {
	return &Service{store: st, pub: pub, exchange: exchange}
}

// Target selects instances either by name or by service; exactly one must be
// set.
type Target struct {
	Instances []string `json:"instances,omitempty"`
	Service   string   `json:"service,omitempty"`
}

type UpRequest struct {
	RequestedBy string            `json:"-"`
	TemplateID  int64             `json:"template_id"`
	ConfigName  string            `json:"config_name"`
	Variables   map[string]string `json:"variables"`
	Target
}

type DownRequest struct {
	RequestedBy string `json:"-"`
	ConfigName  string `json:"config_name"`
	Target
}

func invalid(path, msg string) error { return &nginx.ValidationError{Path: path, Msg: msg} }

// Render loads a template and renders it with values merged over defaults.
func (s *Service) Render(ctx context.Context, templateID int64, values map[string]string) (string, map[string]string, error) {
	t, err := s.store.GetTemplate(ctx, templateID)
	if err != nil {
		return "", nil, err
	}
	vars, err := nginx.ResolveVariables(t.Directives, t.Variables, values)
	if err != nil {
		return "", nil, err
	}
	text, err := nginx.Render(t.Directives, vars)
	if err != nil {
		return "", nil, err
	}
	header := "# Managed by openresty-manager: template " + t.Name + ". Do not edit by hand.\n"
	return header + text, vars, nil
}

func (s *Service) Up(ctx context.Context, req UpRequest) ([]store.Deployment, error) {
	if err := validateConfigName(req.ConfigName); err != nil {
		return nil, err
	}
	if req.TemplateID == 0 {
		return nil, invalid("template_id", "required")
	}
	text, vars, err := s.Render(ctx, req.TemplateID, req.Variables)
	if errors.Is(err, store.ErrNotFound) {
		return nil, invalid("template_id", "template not found")
	}
	if err != nil {
		return nil, err
	}
	targets, err := s.resolveTargets(ctx, req.Target)
	if err != nil {
		return nil, err
	}
	ds := make([]store.Deployment, len(targets))
	for i, in := range targets {
		ds[i] = store.Deployment{
			InstanceID: in.ID, InstanceName: in.Name, Service: in.Service,
			TemplateID: &req.TemplateID, ConfigName: req.ConfigName, Action: store.ActionUp,
			Variables: vars, RenderedConfig: &text, RequestedBy: req.RequestedBy,
		}
	}
	return s.dispatch(ctx, ds)
}

func (s *Service) Down(ctx context.Context, req DownRequest) ([]store.Deployment, error) {
	if err := validateConfigName(req.ConfigName); err != nil {
		return nil, err
	}
	targets, err := s.resolveTargets(ctx, req.Target)
	if err != nil {
		return nil, err
	}
	ds := make([]store.Deployment, len(targets))
	for i, in := range targets {
		ds[i] = store.Deployment{
			InstanceID: in.ID, InstanceName: in.Name, Service: in.Service,
			ConfigName: req.ConfigName, Action: store.ActionDown, RequestedBy: req.RequestedBy,
		}
	}
	return s.dispatch(ctx, ds)
}

// Remove deletes a deployment. If its config file is live on the instance,
// it issues a down that removes the file and returns that down; the
// deployment's row is deleted once the down is applied. Otherwise the row is
// deleted right away and Remove returns nil.
func (s *Service) Remove(ctx context.Context, id int64, requestedBy string) (*store.Deployment, error) {
	live, err := s.store.RemoveDeployment(ctx, id)
	if err != nil || live == nil {
		return nil, err
	}
	ds, err := s.dispatch(ctx, []store.Deployment{{
		InstanceID: live.InstanceID, InstanceName: live.InstanceName, Service: live.Service,
		ConfigName: live.ConfigName, Action: store.ActionDown, RequestedBy: requestedBy,
		RemovesDeploymentID: &live.ID,
	}})
	if err != nil {
		return nil, err
	}
	return &ds[0], nil
}

func validateConfigName(name string) error {
	if !nginx.ResourceNameRe.MatchString(name) {
		return invalid("config_name", "must match "+nginx.ResourceNameRe.String())
	}
	return nil
}

func (s *Service) resolveTargets(ctx context.Context, t Target) ([]store.Instance, error) {
	switch {
	case len(t.Instances) > 0 && t.Service != "":
		return nil, invalid("instances", "set either instances or service, not both")
	case len(t.Instances) > 0:
		found, missing, err := s.store.InstancesByName(ctx, t.Instances)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			return nil, invalid("instances", "unknown instances: "+strings.Join(missing, ", "))
		}
		return found, nil
	case t.Service != "":
		found, err := s.store.ListInstances(ctx, t.Service)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, invalid("service", "no instances registered for service "+t.Service)
		}
		return found, nil
	default:
		return nil, invalid("instances", "set instances or service")
	}
}

// dispatch stores the deployments, then publishes one command per instance.
// Rows are written first so an agent's result can always find its row.
func (s *Service) dispatch(ctx context.Context, ds []store.Deployment) ([]store.Deployment, error) {
	if err := s.store.CreateDeployments(ctx, ds); err != nil {
		return nil, err
	}
	for i := range ds {
		d := &ds[i]
		cmd := mq.Command{
			DeploymentID: d.ID, Action: d.Action, Service: d.Service, Instance: d.InstanceName,
			ConfigName: d.ConfigName, IssuedAt: time.Now().UTC(),
		}
		if d.RenderedConfig != nil {
			cmd.Config = *d.RenderedConfig
		}
		err := s.pub.WithChannel(func(ch *amqp.Channel) error {
			_, err := mq.DeclareAgentQueue(ch, s.exchange, d.Service, d.InstanceName)
			return err
		})
		if err == nil {
			pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = s.pub.Publish(pubCtx, mq.CommandKey(d.Service, d.InstanceName), cmd)
			cancel()
		}
		if err != nil {
			slog.Error("publish command failed", "deployment", d.ID, "err", err)
			d.Status, d.Error = store.StatusFailed, "publish: "+err.Error()
			if _, ferr := s.store.FinishDeployment(context.WithoutCancel(ctx), d.ID, d.Status, d.Error); ferr != nil {
				return nil, ferr
			}
			continue
		}
		if err := s.store.MarkPublished(ctx, d.ID); err != nil {
			return nil, err
		}
		d.Status = store.StatusPublished
	}
	return ds, nil
}

// HandleEvent processes one message from the API events queue.
func (s *Service) HandleEvent(ctx context.Context, d amqp.Delivery) error {
	switch {
	case strings.HasPrefix(d.RoutingKey, "status."):
		var r mq.Result
		if err := json.Unmarshal(d.Body, &r); err != nil {
			slog.Error("dropping malformed result", "err", err)
			return nil
		}
		status := store.StatusApplied
		if !r.Success {
			status = store.StatusFailed
		}
		changed, err := s.store.FinishDeployment(ctx, r.DeploymentID, status, r.Error)
		if err != nil {
			return err
		}
		slog.Info("deployment result", "deployment", r.DeploymentID, "instance", r.Instance,
			"status", status, "error", r.Error, "recorded", changed)
		// A result is also proof of life.
		if !nginx.ResourceNameRe.MatchString(r.Instance) || !nginx.ResourceNameRe.MatchString(r.Service) {
			return nil
		}
		return s.store.TouchInstance(ctx, r.Instance, r.Service, "", r.FinishedAt)
	case strings.HasPrefix(d.RoutingKey, "heartbeat."):
		var h mq.Heartbeat
		if err := json.Unmarshal(d.Body, &h); err != nil {
			slog.Error("dropping malformed heartbeat", "err", err)
			return nil
		}
		if !nginx.ResourceNameRe.MatchString(h.Instance) || !nginx.ResourceNameRe.MatchString(h.Service) {
			slog.Error("dropping heartbeat with invalid names", "instance", h.Instance, "service", h.Service)
			return nil
		}
		return s.store.TouchInstance(ctx, h.Instance, h.Service, h.Host, h.At)
	default:
		slog.Warn("ignoring unexpected routing key", "key", d.RoutingKey)
		return nil
	}
}
