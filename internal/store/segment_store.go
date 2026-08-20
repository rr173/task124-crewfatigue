package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// segCols is the canonical flight_segments column list, in scan order.
const segCols = `id, trip_id, aircraft_type, dep_airport, arr_airport, scheduled_dep, scheduled_arr, actual_dep, actual_arr, block_time_min`

// CreateSegment inserts a flight segment for a trip.
func CreateSegment(ctx context.Context, tx DBTX, s *domain.FlightSegment) (int64, error) {
	if s.TripID <= 0 {
		return 0, fmt.Errorf("%w: trip_id must be positive", domain.ErrInvariantViolation)
	}
	if s.AircraftType == "" {
		return 0, fmt.Errorf("%w: aircraft_type empty", domain.ErrInvariantViolation)
	}
	if s.BlockTimeMin < 0 {
		return 0, fmt.Errorf("%w: block_time negative", domain.ErrInvariantViolation)
	}
	if !s.ScheduledArr.After(s.ScheduledDep) {
		return 0, fmt.Errorf("%w: scheduled_arr must be after scheduled_dep", domain.ErrInvariantViolation)
	}
	// Derive block time from scheduled times if not supplied.
	if s.BlockTimeMin == 0 {
		s.BlockTimeMin = int(s.ScheduledArr.Sub(s.ScheduledDep).Minutes())
	}
	var actualDep, actualArr any
	if !s.ActualDep.IsZero() {
		actualDep = s.ActualDep.UTC().Format(time.RFC3339Nano)
	}
	if !s.ActualArr.IsZero() {
		actualArr = s.ActualArr.UTC().Format(time.RFC3339Nano)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO flight_segments
		(trip_id, aircraft_type, dep_airport, arr_airport, scheduled_dep, scheduled_arr, actual_dep, actual_arr, block_time_min)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.TripID, s.AircraftType, s.DepAirport, s.ArrAirport,
		s.ScheduledDep.UTC().Format(time.RFC3339Nano),
		s.ScheduledArr.UTC().Format(time.RFC3339Nano),
		actualDep, actualArr, s.BlockTimeMin)
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	s.ID = id
	return id, nil
}

// GetSegment loads a flight segment by ID.
func GetSegment(ctx context.Context, tx DBTX, id int64) (*domain.FlightSegment, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+segCols+` FROM flight_segments WHERE id = ?`, id)
	return scanSegment(row)
}

// ListSegmentsByTrip lists all segments of a trip, ordered by scheduled_dep.
func ListSegmentsByTrip(ctx context.Context, tx DBTX, tripID int64) ([]*domain.FlightSegment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+segCols+` FROM flight_segments WHERE trip_id = ? ORDER BY scheduled_dep ASC`, tripID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.FlightSegment
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListLandedSegmentsBetween returns all segments with a non-empty actual_dep
// within the (from, to] UTC window. Used by the cumulative-window rebuild to
// attribute a landed segment's block time to the window its actual departure
// falls into.
func ListLandedSegmentsBetween(ctx context.Context, tx DBTX, crewID int64, from, to time.Time) ([]*domain.FlightSegment, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.`+segCols+` FROM flight_segments s
		JOIN trips t ON t.id = s.trip_id
		WHERE t.crew_id = ?
		  AND s.actual_dep <> ''
		  AND s.actual_dep <> ''
		  AND s.actual_dep > ?
		  AND s.actual_dep <= ?
		ORDER BY s.actual_dep ASC`,
		crewID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.FlightSegment
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanSegment scans one flight_segments row from *sql.Row or *sql.Rows.
func scanSegment(s scanner) (*domain.FlightSegment, error) {
	var seg domain.FlightSegment
	var sdep, sarr string
	var adep, aarr sql.NullString
	if err := s.Scan(&seg.ID, &seg.TripID, &seg.AircraftType, &seg.DepAirport, &seg.ArrAirport,
		&sdep, &sarr, &adep, &aarr, &seg.BlockTimeMin); err != nil {
		return nil, mapErr(err)
	}
	seg.ScheduledDep, _ = time.Parse(time.RFC3339Nano, sdep)
	seg.ScheduledArr, _ = time.Parse(time.RFC3339Nano, sarr)
	seg.ScheduledDep = seg.ScheduledDep.UTC()
	seg.ScheduledArr = seg.ScheduledArr.UTC()
	if adep.Valid && adep.String != "" {
		seg.ActualDep, _ = time.Parse(time.RFC3339Nano, adep.String)
		seg.ActualDep = seg.ActualDep.UTC()
	}
	if aarr.Valid && aarr.String != "" {
		seg.ActualArr, _ = time.Parse(time.RFC3339Nano, aarr.String)
		seg.ActualArr = seg.ActualArr.UTC()
	}
	return &seg, nil
}
