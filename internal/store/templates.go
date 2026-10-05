package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
)

type Template struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Variables   []nginx.Variable  `json:"variables"`
	Directives  []nginx.Directive `json:"directives,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// DirectivePatch updates a single directive. Nil fields are left unchanged;
// a non-nil Children replaces the directive's whole subtree.
type DirectivePatch struct {
	Name     *string            `json:"name"`
	Args     *[]string          `json:"args"`
	Block    *bool              `json:"block"`
	Raw      *string            `json:"raw"`
	Comment  *string            `json:"comment"`
	Position *int               `json:"position"`
	Children *[]nginx.Directive `json:"children"`
}

func (s *Store) CreateTemplate(ctx context.Context, t *Template) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO templates (name, description) VALUES ($1, $2)
			RETURNING id, created_at, updated_at`, t.Name, t.Description,
		).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return mapErr(err)
		}
		if err := insertVariables(ctx, tx, t.ID, t.Variables); err != nil {
			return err
		}
		return insertDirectives(ctx, tx, t.ID, nil, t.Directives)
	})
}

// ReplaceTemplate overwrites a template's metadata, variables and directive
// tree. Directive IDs are reassigned.
func (s *Store) ReplaceTemplate(ctx context.Context, t *Template) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE templates SET name = $2, description = $3, updated_at = now()
			WHERE id = $1 RETURNING created_at, updated_at`, t.ID, t.Name, t.Description,
		).Scan(&t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM template_variables WHERE template_id = $1`, t.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM directives WHERE template_id = $1`, t.ID); err != nil {
			return err
		}
		if err := insertVariables(ctx, tx, t.ID, t.Variables); err != nil {
			return err
		}
		return insertDirectives(ctx, tx, t.ID, nil, t.Directives)
	})
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, created_at, updated_at FROM templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	ts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Template, error) {
		var t Template
		err := r.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	for i := range ts {
		if ts[i].Variables, err = loadVariables(ctx, s.pool, ts[i].ID); err != nil {
			return nil, err
		}
	}
	return ts, nil
}

func (s *Store) GetTemplate(ctx context.Context, id int64) (*Template, error) {
	t := &Template{ID: id}
	err := s.pool.QueryRow(ctx, `
		SELECT name, description, created_at, updated_at FROM templates WHERE id = $1`, id,
	).Scan(&t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if t.Variables, err = loadVariables(ctx, s.pool, id); err != nil {
		return nil, err
	}
	all, err := loadDirectives(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	t.Directives = buildTree(all, nil)
	return t, nil
}

func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDirective returns one directive with its subtree.
func (s *Store) GetDirective(ctx context.Context, templateID, id int64) (*nginx.Directive, error) {
	all, err := loadDirectives(ctx, s.pool, templateID)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.ID == id {
			d.Children = buildTree(all, &d.ID)
			return &d, nil
		}
	}
	return nil, ErrNotFound
}

// AddDirective inserts d (and its children) under parentID, or at the top
// level when parentID is nil. A nil position appends.
func (s *Store) AddDirective(ctx context.Context, templateID int64, parentID *int64, position *int, d *nginx.Directive) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockTemplate(ctx, tx, templateID); err != nil {
			return err
		}
		if parentID != nil {
			var raw *string
			err := tx.QueryRow(ctx, `SELECT raw_body FROM directives WHERE id = $1 AND template_id = $2`,
				*parentID, templateID).Scan(&raw)
			if err != nil {
				if err == pgx.ErrNoRows {
					return &nginx.ValidationError{Path: "parent_id", Msg: "parent directive not found in this template"}
				}
				return err
			}
			if raw != nil {
				return &nginx.ValidationError{Path: "parent_id", Msg: "parent has a raw body and cannot have children"}
			}
			if _, err := tx.Exec(ctx, `UPDATE directives SET is_block = TRUE WHERE id = $1`, *parentID); err != nil {
				return err
			}
		}

		siblings, err := siblingIDs(ctx, tx, templateID, parentID)
		if err != nil {
			return err
		}
		pos := len(siblings)
		if position != nil && *position >= 0 && *position < pos {
			pos = *position
		}
		if _, err := tx.Exec(ctx, `
			UPDATE directives SET position = position + 1
			WHERE template_id = $1 AND parent_id IS NOT DISTINCT FROM $2 AND position >= $3`,
			templateID, parentID, pos); err != nil {
			return err
		}
		d.ParentID = parentID
		d.Position = pos
		if err := insertDirective(ctx, tx, templateID, d); err != nil {
			return err
		}
		return normalizePositions(ctx, tx, templateID, parentID)
	})
}

func (s *Store) UpdateDirective(ctx context.Context, templateID, id int64, p *DirectivePatch) (*nginx.Directive, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockTemplate(ctx, tx, templateID); err != nil {
			return err
		}
		var cur nginx.Directive
		err := tx.QueryRow(ctx, `
			SELECT id, parent_id, position, name, args, is_block, raw_body, comment
			FROM directives WHERE id = $1 AND template_id = $2`, id, templateID,
		).Scan(&cur.ID, &cur.ParentID, &cur.Position, &cur.Name, &cur.Args, &cur.Block, &cur.Raw, &cur.Comment)
		if err != nil {
			return mapErr(err)
		}
		var childCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM directives WHERE parent_id = $1`, id).Scan(&childCount); err != nil {
			return err
		}

		if p.Name != nil {
			cur.Name = *p.Name
		}
		if p.Args != nil {
			cur.Args = *p.Args
		}
		if p.Block != nil {
			cur.Block = *p.Block
		}
		if p.Raw != nil {
			cur.Raw = p.Raw
			if *p.Raw == "" { // empty string clears the raw body
				cur.Raw = nil
			}
		}
		if p.Comment != nil {
			cur.Comment = *p.Comment
		}
		if p.Children != nil {
			cur.Children = *p.Children
			childCount = len(cur.Children)
		}
		if cur.Raw != nil && childCount > 0 {
			return &nginx.ValidationError{Msg: "a directive cannot have both raw and children"}
		}
		if err := nginx.ValidateDirective(&cur); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE directives SET name = $2, args = $3, is_block = $4, raw_body = $5, comment = $6
			WHERE id = $1`, id, cur.Name, nonNil(cur.Args), cur.Block || childCount > 0, cur.Raw, cur.Comment); err != nil {
			return err
		}
		if p.Children != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM directives WHERE parent_id = $1`, id); err != nil {
				return err
			}
			if err := insertDirectives(ctx, tx, templateID, &id, cur.Children); err != nil {
				return err
			}
		}
		if p.Position != nil && *p.Position != cur.Position {
			if err := moveWithinSiblings(ctx, tx, templateID, cur.ParentID, id, *p.Position); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetDirective(ctx, templateID, id)
}

func (s *Store) DeleteDirective(ctx context.Context, templateID, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockTemplate(ctx, tx, templateID); err != nil {
			return err
		}
		var parentID *int64
		err := tx.QueryRow(ctx, `DELETE FROM directives WHERE id = $1 AND template_id = $2 RETURNING parent_id`,
			id, templateID).Scan(&parentID)
		if err != nil {
			return mapErr(err)
		}
		return normalizePositions(ctx, tx, templateID, parentID)
	})
}

// lockTemplate serialises structural edits of one template and bumps its
// updated_at.
func lockTemplate(ctx context.Context, tx pgx.Tx, templateID int64) error {
	tag, err := tx.Exec(ctx, `UPDATE templates SET updated_at = now() WHERE id = $1`, templateID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func insertVariables(ctx context.Context, tx pgx.Tx, templateID int64, vs []nginx.Variable) error {
	for _, v := range vs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO template_variables (template_id, name, default_value, required, description)
			VALUES ($1, $2, $3, $4, $5)`, templateID, v.Name, v.Default, v.Required, v.Description); err != nil {
			return err
		}
	}
	return nil
}

func insertDirectives(ctx context.Context, tx pgx.Tx, templateID int64, parentID *int64, ds []nginx.Directive) error {
	for i := range ds {
		ds[i].ParentID = parentID
		ds[i].Position = i
		if err := insertDirective(ctx, tx, templateID, &ds[i]); err != nil {
			return err
		}
	}
	return nil
}

// insertDirective inserts d and, recursively, its children, filling in IDs.
func insertDirective(ctx context.Context, tx pgx.Tx, templateID int64, d *nginx.Directive) error {
	err := tx.QueryRow(ctx, `
		INSERT INTO directives (template_id, parent_id, position, name, args, is_block, raw_body, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		templateID, d.ParentID, d.Position, d.Name, nonNil(d.Args), d.IsBlock(), d.Raw, d.Comment,
	).Scan(&d.ID)
	if err != nil {
		return err
	}
	d.Block = d.IsBlock()
	return insertDirectives(ctx, tx, templateID, &d.ID, d.Children)
}

func siblingIDs(ctx context.Context, tx pgx.Tx, templateID int64, parentID *int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT id FROM directives
		WHERE template_id = $1 AND parent_id IS NOT DISTINCT FROM $2
		ORDER BY position, id`, templateID, parentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func writePositions(ctx context.Context, tx pgx.Tx, ids []int64) error {
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE directives SET position = $2 WHERE id = $1 AND position <> $2`, id, i); err != nil {
			return err
		}
	}
	return nil
}

func normalizePositions(ctx context.Context, tx pgx.Tx, templateID int64, parentID *int64) error {
	ids, err := siblingIDs(ctx, tx, templateID, parentID)
	if err != nil {
		return err
	}
	return writePositions(ctx, tx, ids)
}

func moveWithinSiblings(ctx context.Context, tx pgx.Tx, templateID int64, parentID *int64, id int64, to int) error {
	ids, err := siblingIDs(ctx, tx, templateID, parentID)
	if err != nil {
		return err
	}
	rest := make([]int64, 0, len(ids))
	for _, x := range ids {
		if x != id {
			rest = append(rest, x)
		}
	}
	to = max(0, min(to, len(rest)))
	ordered := append(append(append([]int64{}, rest[:to]...), id), rest[to:]...)
	return writePositions(ctx, tx, ordered)
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadVariables(ctx context.Context, q querier, templateID int64) ([]nginx.Variable, error) {
	rows, err := q.Query(ctx, `
		SELECT name, default_value, required, description
		FROM template_variables WHERE template_id = $1 ORDER BY name`, templateID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (nginx.Variable, error) {
		var v nginx.Variable
		err := r.Scan(&v.Name, &v.Default, &v.Required, &v.Description)
		return v, err
	})
}

func loadDirectives(ctx context.Context, q querier, templateID int64) ([]nginx.Directive, error) {
	rows, err := q.Query(ctx, `
		SELECT id, parent_id, position, name, args, is_block, raw_body, comment
		FROM directives WHERE template_id = $1 ORDER BY position, id`, templateID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (nginx.Directive, error) {
		var d nginx.Directive
		err := r.Scan(&d.ID, &d.ParentID, &d.Position, &d.Name, &d.Args, &d.Block, &d.Raw, &d.Comment)
		return d, err
	})
}

// buildTree assembles the children of parent from a flat, position-ordered
// list.
func buildTree(all []nginx.Directive, parent *int64) []nginx.Directive {
	byParent := make(map[int64][]nginx.Directive)
	var roots []nginx.Directive
	for _, d := range all {
		if d.ParentID == nil {
			roots = append(roots, d)
		} else {
			byParent[*d.ParentID] = append(byParent[*d.ParentID], d)
		}
	}
	var attach func([]nginx.Directive) []nginx.Directive
	attach = func(ds []nginx.Directive) []nginx.Directive {
		for i := range ds {
			ds[i].Children = attach(byParent[ds[i].ID])
		}
		return ds
	}
	if parent == nil {
		return attach(roots)
	}
	return attach(byParent[*parent])
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}
