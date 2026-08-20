package store

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// restCols is the canonical rest_periods column list, in scan order.
const restCols = `id, crew_id, start, end, rest_type, duration_min, covers_weekly`

// CreateRest inserts a rest period.
func CreateRest(ctx context.Context, tx DBTX, r *domain.RestPeriod) (int64, error) {
	if r.CrewID <= 0 {
		return 0, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if !r.RestType.IsValid() {
		return 0, fmt.Errorf("%w: rest_type %q", domain.ErrRestTypeInvalid, r.RestType)
	}
	if !r.End.After(r.Start) {
		return 0, fmt.Errorf("%w: end must be after start", domain.ErrInvariantViolation)
	}
	if r.DurationMin == 0 {
		r.DurationMin = int(r.End.Sub(r.Start).Minutes())
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO rest_periods
		(crew_id, start, end, rest_type, duration_min, covers_weekly) VALUES (?,?,?,?,?,?)`,
		r.CrewID, r.Start.UTC().Format(time.RFC3339Nano), r.End.UTC().Format(time.RFC3339Nano),
		string(r.RestType), r.DurationMin, boolToInt(r.CoversWeekly))
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	r.ID = id
	return id, nil
}

// GetRest loads a rest period by ID.
func GetRest(ctx context.Context, tx DBTX, id int64) (*domain.RestPeriod, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+restCols+` FROM rest_periods WHERE id = ?`, id)
	return scanRest(row)
}

// ListRest lists rest periods optionally filtered by crew_id, ordered by start.
func ListRest(ctx context.Context, tx DBTX, crewID int64) ([]*domain.RestPeriod, error) {
	q := `SELECT ` + restCols + ` FROM rest_periods`
	args := []any{}
	if crewID > 0 {
		q += ` WHERE crew_id = ?`
		args = append(args, crewID)
	}
	q += ` ORDER BY start ASC`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.RestPeriod
	for rows.Next() {
		r, err := scanRest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRestCompletedBetween returns rest periods completed within (from, to].
// Used by the R8 weekly-rest coverage check.
func ListRestCompletedBetween(ctx context.Context, tx DBTX, crewID int64, from, to time.Time) ([]*domain.RestPeriod, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+restCols+` FROM rest_periods
		WHERE crew_id = ? AND end > ? AND end <= ? ORDER BY start ASC`,
		crewID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.RestPeriod
	for rows.Next() {
		r, err := scanRest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastRestBefore returns the most recent completed rest period ending at or
// before t for a crew member, used to measure rest-since-last-duty.
func LastRestBefore(ctx context.Context, tx DBTX, crewID int64, t time.Time) (*domain.RestPeriod, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+restCols+` FROM rest_periods
		WHERE crew_id = ? AND end <= ? ORDER BY end DESC LIMIT 1`,
		crewID, t.UTC().Format(time.RFC3339Nano))
	r, err := scanRest(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return r, nil
}

// scanRest scans one rest_periods row.
func scanRest(s scanner) (*domain.RestPeriod, error) {
	var r domain.RestPeriod
	var covers int
	var start, end string
	if err := s.Scan(&r.ID, &r.CrewID, &start, &end, &r.RestType, &r.DurationMin, &covers); err != nil {
		return nil, mapErr(err)
	}
	r.CoversWeekly = covers != 0
	if start != "" {
		t, err := time.Parse(time.RFC3339Nano, start)
		if err == nil {
			r.Start = t.UTC()
		}
	}
	if end != "" {
		t, err := time.Parse(time.RFC3339Nano, end)
		if err == nil {
			r.End = t.UTC()
		}
	}
	return &r, nil
}
