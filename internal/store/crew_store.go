package store

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// crewCols is the canonical crew_members column list, in scan order.
const crewCols = `id, name, role, home_base, home_tz, active, created_at`

// CreateCrew inserts a crew member. name+role must be unique (DB-enforced).
func CreateCrew(ctx context.Context, tx DBTX, c *domain.CrewMember) (int64, error) {
	if !c.Role.IsValid() {
		return 0, fmt.Errorf("%w: role %q", domain.ErrRoleInvalid, c.Role)
	}
	if _, err := time.LoadLocation(c.HomeTZ); err != nil {
		return 0, fmt.Errorf("%w: %v", domain.ErrTZInvalid, err)
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = nowUTC()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO crew_members
		(name, role, home_base, home_tz, active, created_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(name, role) DO NOTHING`,
		c.Name, string(c.Role), c.HomeBase, c.HomeTZ, boolToInt(c.Active), c.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, mapErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fmt.Errorf("%w: name+role already exists", domain.ErrCrewExists)
	}
	id, _ := res.LastInsertId()
	c.ID = id
	return id, nil
}

// GetCrew loads a crew member by ID.
func GetCrew(ctx context.Context, tx DBTX, id int64) (*domain.CrewMember, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+crewCols+` FROM crew_members WHERE id = ?`, id)
	return scanCrew(row)
}

// GetCrewByNameRole loads a crew member by (name, role).
func GetCrewByNameRole(ctx context.Context, tx DBTX, name string, role domain.CrewRole) (*domain.CrewMember, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+crewCols+` FROM crew_members WHERE name = ? AND role = ?`, name, string(role))
	return scanCrew(row)
}

// ListCrew lists crew members optionally filtered by role/active.
func ListCrew(ctx context.Context, tx DBTX, role string, active *bool) ([]*domain.CrewMember, error) {
	q := `SELECT ` + crewCols + ` FROM crew_members WHERE 1=1`
	args := []any{}
	if role != "" {
		q += ` AND role = ?`
		args = append(args, role)
	}
	if active != nil {
		q += ` AND active = ?`
		args = append(args, boolToInt(*active))
	}
	q += ` ORDER BY id ASC`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.CrewMember
	for rows.Next() {
		c, err := scanCrew(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCrew patches mutable crew fields (name, home_base, home_tz, active).
func UpdateCrew(ctx context.Context, tx DBTX, c *domain.CrewMember) error {
	if !c.Role.IsValid() {
		return fmt.Errorf("%w: role %q", domain.ErrRoleInvalid, c.Role)
	}
	if _, err := time.LoadLocation(c.HomeTZ); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrTZInvalid, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE crew_members SET name=?, home_base=?, home_tz=?, active=? WHERE id=?`,
		c.Name, c.HomeBase, c.HomeTZ, boolToInt(c.Active), c.ID)
	if err != nil {
		return mapErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrCrewNotFound
	}
	return nil
}

// scanCrew scans one crew_members row from either *sql.Row or *sql.Rows.
func scanCrew(s scanner) (*domain.CrewMember, error) {
	var c domain.CrewMember
	var active int
	var created string
	if err := s.Scan(&c.ID, &c.Name, &c.Role, &c.HomeBase, &c.HomeTZ, &active, &created); err != nil {
		return nil, mapErr(err)
	}
	c.Active = active != 0
	if created != "" {
		t, err := time.Parse(time.RFC3339Nano, created)
		if err == nil {
			c.CreatedAt = t.UTC()
		}
	}
	return &c, nil
}
