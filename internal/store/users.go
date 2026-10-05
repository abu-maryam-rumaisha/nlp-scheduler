package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownRole is returned when a user is given a role not in the roles
// table.
var ErrUnknownRole = errors.New("unknown role")

type Role struct {
	ID          int16     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const userSelect = `
	SELECT u.id, u.username, u.display_name, u.email, r.name, u.password_hash, COALESCE(u.totp_secret, ''), u.created_at, u.updated_at
	FROM users u JOIN roles r ON r.id = u.role_id`

func scanUser(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.PasswordHash, &u.TOTPSecret, &u.CreatedAt, &u.UpdatedAt)
	u.TOTPEnabled = u.TOTPSecret != ""
	return u, err
}

func (s *Store) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, description, created_at FROM roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Role, error) {
		var ro Role
		err := r.Scan(&ro.ID, &ro.Name, &ro.Description, &ro.CreatedAt)
		return ro, err
	})
}

func roleID(ctx context.Context, q pgx.Tx, name string) (int16, error) {
	var id int16
	err := q.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUnknownRole
	}
	return id, err
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rid, err := roleID(ctx, tx, u.Role)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, role_id) VALUES ($1, $2, $3) RETURNING id`,
			u.Username, u.PasswordHash, rid).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		created, err := scanUser(tx.QueryRow(ctx, userSelect+` WHERE u.id = $1`, id))
		*u = created
		return err
	})
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, userSelect+` WHERE u.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, userSelect+` WHERE u.username = $1`, username))
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, userSelect+` ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser changes the role and/or password hash; nil leaves a field as is.
func (s *Store) UpdateUser(ctx context.Context, id int64, role, passwordHash *string) (*User, error) {
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var rid *int16
		if role != nil {
			r, err := roleID(ctx, tx, *role)
			if err != nil {
				return err
			}
			rid = &r
		}
		tag, err := tx.Exec(ctx, `
			UPDATE users SET
				role_id = COALESCE($2, role_id),
				password_hash = COALESCE($3, password_hash),
				updated_at = now()
			WHERE id = $1`, id, rid, passwordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		u, err = scanUser(tx.QueryRow(ctx, userSelect+` WHERE u.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateProfile sets the user's display name and email.
func (s *Store) UpdateProfile(ctx context.Context, id int64, displayName, email string) (*User, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET display_name = $2, email = $3, updated_at = now() WHERE id = $1`,
		id, displayName, email)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetUser(ctx, id)
}

// SetTOTPSecret turns two-factor authentication on with secret, or off when
// secret is empty.
func (s *Store) SetTOTPSecret(ctx context.Context, id int64, secret string) (*User, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET totp_secret = NULLIF($2, ''), totp_last_step = 0, updated_at = now()
		WHERE id = $1`, id, secret)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetUser(ctx, id)
}

// UseTOTPStep records that a code for the given time step was accepted. It
// returns false if a code for that step or a later one was already used, so
// each code works only once.
func (s *Store) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2`, id, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
