package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ActionUp   = "up"
	ActionDown = "down"

	StatusPending   = "pending"   // row written, message not yet confirmed by the broker
	StatusPublished = "published" // broker accepted the command
	StatusApplied   = "applied"   // agent wrote the file, config test and reload passed
	StatusFailed    = "failed"    // publish or apply failed; see Error
)

// ErrInProgress is returned when removing a deployment that has not
// finished, or whose removal is already under way.
var ErrInProgress = errors.New("deployment in progress")

type Deployment struct {
	ID             int64             `json:"id"`
	InstanceID     int64             `json:"instance_id"`
	InstanceName   string            `json:"instance"`
	Service        string            `json:"service"`
	TemplateID     *int64            `json:"template_id"`
	ConfigName     string            `json:"config_name"`
	Action         string            `json:"action"`
	Variables      map[string]string `json:"variables"`
	RenderedConfig *string           `json:"rendered_config,omitempty"`
	Status         string            `json:"status"`
	Error          string            `json:"error,omitempty"`
	RequestedBy    string            `json:"requested_by"`
	// RemovesDeploymentID is set on a down issued to remove that deployment.
	RemovesDeploymentID *int64    `json:"removes_deployment_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type DeploymentFilter struct {
	InstanceID int64
	ConfigName string
	Status     string
	Limit      int // page size, 1–500 (default 100)
	Offset     int
}

const deploymentSelect = `
	SELECT d.id, d.instance_id, i.name, i.service, d.template_id, d.config_name, d.action,
	       d.variables, d.rendered_config, d.status, d.error, d.requested_by, d.removes_deployment_id,
	       d.created_at, d.updated_at
	FROM deployments d JOIN instances i ON i.id = d.instance_id`

func scanDeployment(r pgx.Row) (Deployment, error) {
	var d Deployment
	err := r.Scan(&d.ID, &d.InstanceID, &d.InstanceName, &d.Service, &d.TemplateID, &d.ConfigName, &d.Action,
		&d.Variables, &d.RenderedConfig, &d.Status, &d.Error, &d.RequestedBy, &d.RemovesDeploymentID,
		&d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// CreateDeployments inserts all rows in one transaction, filling in IDs.
func (s *Store) CreateDeployments(ctx context.Context, ds []Deployment) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for i := range ds {
			d := &ds[i]
			if d.Variables == nil {
				d.Variables = map[string]string{}
			}
			d.Status = StatusPending
			err := tx.QueryRow(ctx, `
				INSERT INTO deployments (instance_id, template_id, config_name, action, variables, rendered_config,
				                         requested_by, removes_deployment_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at, updated_at`,
				d.InstanceID, d.TemplateID, d.ConfigName, d.Action, d.Variables, d.RenderedConfig, d.RequestedBy,
				d.RemovesDeploymentID,
			).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkPublished moves a deployment from pending to published. It is a no-op
// if the agent's result already arrived.
func (s *Store) MarkPublished(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET status = 'published', updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id)
	return err
}

// FinishDeployment records the agent's result. On success it also advances
// instance_configs, unless a newer deployment of the same config already
// did, and deletes the deployment a removal down was issued for. It returns
// false if the deployment was unknown or already finished.
func (s *Store) FinishDeployment(ctx context.Context, id int64, status, errMsg string) (bool, error) {
	var changed bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var instanceID int64
		var templateID, removes *int64
		var configName, action string
		err := tx.QueryRow(ctx, `
			UPDATE deployments SET status = $2, error = $3, updated_at = now()
			WHERE id = $1 AND status IN ('pending', 'published')
			RETURNING instance_id, template_id, config_name, action, removes_deployment_id`, id, status, errMsg,
		).Scan(&instanceID, &templateID, &configName, &action, &removes)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		changed = true
		if status != StatusApplied {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO instance_configs (instance_id, config_name, template_id, state, deployment_id)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (instance_id, config_name) DO UPDATE SET
				template_id = COALESCE(EXCLUDED.template_id, instance_configs.template_id), state = EXCLUDED.state,
				deployment_id = EXCLUDED.deployment_id, updated_at = now()
			WHERE instance_configs.deployment_id < EXCLUDED.deployment_id`,
			instanceID, configName, templateID, action, id)
		if err != nil || removes == nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, *removes)
		return err
	})
	return changed, err
}

// RemoveDeployment deletes a deployment whose config file is not on the
// instance and returns nil. If the deployment is what the instance currently
// runs for its config, the row is kept and returned: the caller must take the
// config down first, and the row is deleted once that down is applied.
func (s *Store) RemoveDeployment(ctx context.Context, id int64) (*Deployment, error) {
	var live *Deployment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		d, err := scanDeployment(tx.QueryRow(ctx, deploymentSelect+` WHERE d.id = $1 FOR UPDATE OF d`, id))
		if err != nil {
			return mapErr(err)
		}
		if d.Status == StatusPending || d.Status == StatusPublished {
			return ErrInProgress
		}
		var removing bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM deployments
			               WHERE removes_deployment_id = $1 AND status IN ('pending', 'published'))`, id,
		).Scan(&removing); err != nil {
			return err
		}
		if removing {
			return ErrInProgress
		}
		var current bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM instance_configs
			               WHERE instance_id = $1 AND config_name = $2 AND deployment_id = $3 AND state = 'up')`,
			d.InstanceID, d.ConfigName, d.ID,
		).Scan(&current); err != nil {
			return err
		}
		if current {
			live = &d
			return nil
		}
		_, err = tx.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, id)
		return err
	})
	return live, err
}

func (s *Store) GetDeployment(ctx context.Context, id int64) (*Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, deploymentSelect+` WHERE d.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

// ListDeployments returns one page of deployments, newest first and without
// rendered configs, plus the number of deployments matching the filter.
func (s *Store) ListDeployments(ctx context.Context, f DeploymentFilter) ([]Deployment, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	f.Offset = max(f.Offset, 0)
	const where = `
		WHERE ($1 = 0 OR d.instance_id = $1)
		  AND ($2 = '' OR d.config_name = $2)
		  AND ($3 = '' OR d.status = $3)`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM deployments d`+where,
		f.InstanceID, f.ConfigName, f.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, deploymentSelect+where+` ORDER BY d.id DESC LIMIT $4 OFFSET $5`,
		f.InstanceID, f.ConfigName, f.Status, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	ds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Deployment, error) { return scanDeployment(r) })
	if err != nil {
		return nil, 0, err
	}
	for i := range ds {
		ds[i].RenderedConfig = nil
	}
	return ds, total, nil
}
