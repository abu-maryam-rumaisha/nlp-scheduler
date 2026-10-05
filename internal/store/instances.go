package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Instance struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Service     string     `json:"service"`
	Host        string     `json:"host"`
	Description string     `json:"description"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// InstanceConfig is the last applied state of one config on one instance.
type InstanceConfig struct {
	ConfigName   string    `json:"config_name"`
	TemplateID   *int64    `json:"template_id"`
	State        string    `json:"state"`
	DeploymentID int64     `json:"deployment_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const instanceCols = `id, name, service, host, description, last_seen_at, created_at, updated_at`

func scanInstance(r pgx.Row) (Instance, error) {
	var i Instance
	err := r.Scan(&i.ID, &i.Name, &i.Service, &i.Host, &i.Description, &i.LastSeenAt, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (s *Store) CreateInstance(ctx context.Context, in *Instance) error {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO instances (name, service, host, description) VALUES ($1, $2, $3, $4)
		RETURNING `+instanceCols, in.Name, in.Service, in.Host, in.Description)
	created, err := scanInstance(row)
	if err != nil {
		return mapErr(err)
	}
	*in = created
	return nil
}

func (s *Store) UpdateInstance(ctx context.Context, in *Instance) error {
	row := s.pool.QueryRow(ctx, `
		UPDATE instances SET name = $2, service = $3, host = $4, description = $5, updated_at = now()
		WHERE id = $1 RETURNING `+instanceCols, in.ID, in.Name, in.Service, in.Host, in.Description)
	updated, err := scanInstance(row)
	if err != nil {
		return mapErr(err)
	}
	*in = updated
	return nil
}

func (s *Store) GetInstance(ctx context.Context, id int64) (*Instance, error) {
	in, err := scanInstance(s.pool.QueryRow(ctx, `SELECT `+instanceCols+` FROM instances WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &in, nil
}

// ListInstances returns all instances, or only those of service when it is
// non-empty.
func (s *Store) ListInstances(ctx context.Context, service string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instanceCols+` FROM instances
		WHERE $1 = '' OR service = $1 ORDER BY service, name`, service)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Instance, error) { return scanInstance(r) })
}

// InstancesByName returns the named instances that exist and the names that
// do not.
func (s *Store) InstancesByName(ctx context.Context, names []string) ([]Instance, []string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instanceCols+` FROM instances WHERE name = ANY($1) ORDER BY name`, names)
	if err != nil {
		return nil, nil, err
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Instance, error) { return scanInstance(r) })
	if err != nil {
		return nil, nil, err
	}
	have := make(map[string]bool, len(found))
	for _, in := range found {
		have[in.Name] = true
	}
	var missing []string
	for _, n := range names {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	return found, missing, nil
}

func (s *Store) DeleteInstance(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM instances WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchInstance records a heartbeat, registering the instance on first sight.
func (s *Store) TouchInstance(ctx context.Context, name, service, host string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instances (name, service, host, last_seen_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET
			last_seen_at = GREATEST(instances.last_seen_at, EXCLUDED.last_seen_at),
			host = CASE WHEN EXCLUDED.host <> '' THEN EXCLUDED.host ELSE instances.host END`,
		name, service, host, at)
	return err
}

func (s *Store) ListInstanceConfigs(ctx context.Context, instanceID int64) ([]InstanceConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT config_name, template_id, state, deployment_id, updated_at
		FROM instance_configs WHERE instance_id = $1 ORDER BY config_name`, instanceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InstanceConfig, error) {
		var c InstanceConfig
		err := r.Scan(&c.ConfigName, &c.TemplateID, &c.State, &c.DeploymentID, &c.UpdatedAt)
		return c, err
	})
}
