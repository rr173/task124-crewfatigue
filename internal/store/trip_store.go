package store

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// CreateTrip inserts a trip for a crew member.
func CreateTrip(ctx context.Context, tx DBTX, t *domain.Trip) (int64, error) {
	if t.CrewID <= 0 {
		return 0, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = nowUTC()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO trips (crew_id, planned, created_at) VALUES (?,?,?)`,
		t.CrewID, boolToInt(t.Planned), t.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	t.ID = id
	return id, nil
}

// GetTrip loads a trip by ID.
func GetTrip(ctx context.Context, tx DBTX, id int64) (*domain.Trip, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, crew_id, planned, created_at FROM trips WHERE id = ?`, id)
	var t domain.Trip
	var planned int
	var created string
	if err := row.Scan(&t.ID, &t.CrewID, &planned, &created); err != nil {
		return nil, mapErr(err)
	}
	t.Planned = planned != 0
	if created != "" {
		parsed, err := time.Parse(time.RFC3339Nano, created)
		if err == nil {
			t.CreatedAt = parsed.UTC()
		}
	}
	return &t, nil
}

// GetTripForCrew loads a trip only when it belongs to the requested crew.
func GetTripForCrew(ctx context.Context, tx DBTX, tripID, crewID int64) (*domain.Trip, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, crew_id, planned, created_at FROM trips WHERE id = ? AND crew_id = ?`, tripID, crewID)
	var t domain.Trip
	var planned int
	var created string
	if err := row.Scan(&t.ID, &t.CrewID, &planned, &created); err != nil {
		return nil, mapErr(err)
	}
	t.Planned = planned != 0
	if created != "" {
		parsed, err := time.Parse(time.RFC3339Nano, created)
		if err == nil {
			t.CreatedAt = parsed.UTC()
		}
	}
	return &t, nil
}

// ListTrips lists trips optionally filtered by crew_id.
func ListTrips(ctx context.Context, tx DBTX, crewID int64) ([]*domain.Trip, error) {
	q := `SELECT id, crew_id, planned, created_at FROM trips`
	args := []any{}
	if crewID > 0 {
		q += ` WHERE crew_id = ?`
		args = append(args, crewID)
	}
	q += ` ORDER BY id ASC`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.Trip
	for rows.Next() {
		var t domain.Trip
		var planned int
		var created string
		if err := rows.Scan(&t.ID, &t.CrewID, &planned, &created); err != nil {
			return nil, mapErr(err)
		}
		t.Planned = planned != 0
		if created != "" {
			parsed, err := time.Parse(time.RFC3339Nano, created)
			if err == nil {
				t.CreatedAt = parsed.UTC()
			}
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// MarkTripUnplanned flags a trip as closed (planned=0) once its duty period is
// closed and all its segments are landed.
func MarkTripUnplanned(ctx context.Context, tx DBTX, tripID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE trips SET planned=0 WHERE id=?`, tripID)
	return mapErr(err)
}
