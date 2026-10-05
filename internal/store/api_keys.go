package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type APIKey struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Owner      string     `json:"owner"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	KeyHash    []byte     `json:"-"`
}

const apiKeySelect = `
	SELECT k.id, k.user_id, u.username, k.name, k.prefix, r.name, k.expires_at, k.last_used_at, k.created_at, k.key_hash
	FROM api_keys k JOIN users u ON u.id = k.user_id JOIN roles r ON r.id = k.role_id`

func scanAPIKey(r pgx.Row) (APIKey, error) {
	var k APIKey
	err := r.Scan(&k.ID, &k.UserID, &k.Owner, &k.Name, &k.Prefix, &k.Role, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt, &k.KeyHash)
	return k, err
}

// CreateAPIKey stores a key. Prefix and KeyHash must be set; the rest of k is
// filled in from the database.
func (s *Store) CreateAPIKey(ctx context.Context, k *APIKey) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rid, err := roleID(ctx, tx, k.Role)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(ctx, `
			INSERT INTO api_keys (user_id, name, prefix, key_hash, role_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			k.UserID, k.Name, k.Prefix, k.KeyHash, rid, k.ExpiresAt).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		created, err := scanAPIKey(tx.QueryRow(ctx, apiKeySelect+` WHERE k.id = $1`, id))
		*k = created
		return err
	})
}

// ListAPIKeys returns the keys of one user, or of everyone when userID is 0,
// newest first.
func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, apiKeySelect+`
		WHERE $1 = 0 OR k.user_id = $1 ORDER BY k.created_at DESC, k.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKey, error) { return scanAPIKey(r) })
}

func (s *Store) GetAPIKey(ctx context.Context, id int64) (*APIKey, error) {
	k, err := scanAPIKey(s.pool.QueryRow(ctx, apiKeySelect+` WHERE k.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &k, nil
}

func (s *Store) GetAPIKeyByPrefix(ctx context.Context, prefix string) (*APIKey, error) {
	k, err := scanAPIKey(s.pool.QueryRow(ctx, apiKeySelect+` WHERE k.prefix = $1`, prefix))
	if err != nil {
		return nil, mapErr(err)
	}
	return &k, nil
}

func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_keys WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAPIKey records use of a key, at most once a minute to keep writes
// off the hot path.
func (s *Store) TouchAPIKey(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, id)
	return err
}
