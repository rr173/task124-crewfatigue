package store

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// dutyCols is the canonical duty_periods column list, in scan order.
const dutyCols = `id, crew_id, trip_id, report_time, release_time, is_augmented, split_break_min, status, unforeseen_extension_min, owe_augmented_rest`

// CreateDuty inserts a duty period and links its segments in seq order.
func CreateDuty(ctx context.Context, tx DBTX, d *domain.DutyPeriod, segIDs []int64) (int64, error) {
	if d.CrewID <= 0 {
		return 0, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if d.TripID <= 0 {
		return 0, fmt.Errorf("%w: trip_id must be positive", domain.ErrInvariantViolation)
	}
	if len(segIDs) == 0 {
		return 0, domain.ErrDutyEmpty
	}
	if !d.ReleaseTime.After(d.ReportTime) {
		return 0, fmt.Errorf("%w: release_time must be after report_time", domain.ErrInvariantViolation)
	}
	if d.Status == "" {
		d.Status = domain.DutyOpen
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO duty_periods
		(crew_id, trip_id, report_time, release_time, is_augmented, split_break_min, status,
		 unforeseen_extension_min, owe_augmented_rest)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.CrewID, d.TripID, d.ReportTime.UTC().Format(time.RFC3339Nano),
		d.ReleaseTime.UTC().Format(time.RFC3339Nano), boolToInt(d.IsAugmented), d.SplitBreakMin,
		string(d.Status), d.UnforeseenExtensionMin, boolToInt(d.OweAugmentedRest))
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	d.ID = id
	for i, sid := range segIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO duty_period_segments (duty_period_id, segment_id, seq) VALUES (?,?,?)`,
			id, sid, i); err != nil {
			return 0, mapErr(err)
		}
	}
	return id, nil
}

// GetDuty loads a duty period with its linked segments (ordered by seq).
func GetDuty(ctx context.Context, tx DBTX, id int64) (*domain.DutyPeriod, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+dutyCols+` FROM duty_periods WHERE id = ?`, id)
	d, err := scanDuty(row)
	if err != nil {
		return nil, err
	}
	segs, err := ListSegmentsForDuty(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	d.Segments = segs
	return d, nil
}

// ListDutyPeriods lists duty periods optionally filtered by crew_id and/or
// status. Segments are not preloaded; callers needing them use GetDuty.
func ListDutyPeriods(ctx context.Context, tx DBTX, crewID int64, status string) ([]*domain.DutyPeriod, error) {
	q := `SELECT ` + dutyCols + ` FROM duty_periods WHERE 1=1`
	args := []any{}
	if crewID > 0 {
		q += ` AND crew_id = ?`
		args = append(args, crewID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY report_time ASC`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.DutyPeriod
	for rows.Next() {
		d, err := scanDuty(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListClosedDutyReportBetween returns closed duty periods whose report_time is
// within (from, to], used by the R5 168h FDP window rebuild.
func ListClosedDutyReportBetween(ctx context.Context, tx DBTX, crewID int64, from, to time.Time) ([]*domain.DutyPeriod, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+dutyCols+` FROM duty_periods
		WHERE crew_id = ? AND status = ? AND report_time > ? AND report_time <= ? ORDER BY report_time ASC`,
		crewID, string(domain.DutyClosed), from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.DutyPeriod
	for rows.Next() {
		d, err := scanDuty(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CloseDuty transitions a duty period from OPEN to CLOSED. It is called by
// the schedule service after all segments have landed; the caller must hold the
// tx so the close + event append are atomic.
func CloseDuty(ctx context.Context, tx DBTX, id int64, unforeseenMin int, oweAugmented bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE duty_periods SET status=?, unforeseen_extension_min=?, owe_augmented_rest=? WHERE id=? AND status=?`,
		string(domain.DutyClosed), unforeseenMin, boolToInt(oweAugmented), id, string(domain.DutyOpen))
	if err != nil {
		return mapErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrDutyNotOpen
	}
	return nil
}

// SetDutyAugmented updates the augmented flag (used when a relief pilot is
// added/removed on an OPEN duty period).
func SetDutyAugmented(ctx context.Context, tx DBTX, id int64, augmented bool) error {
	res, err := tx.ExecContext(ctx, `UPDATE duty_periods SET is_augmented=? WHERE id=? AND status=?`,
		boolToInt(augmented), id, string(domain.DutyOpen))
	if err != nil {
		return mapErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrDutyClosed
	}
	return nil
}

// ListSegmentsForDuty returns the segments linked to a duty period in seq order.
func ListSegmentsForDuty(ctx context.Context, tx DBTX, dutyID int64) ([]domain.FlightSegment, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.`+segCols+` FROM flight_segments s
		JOIN duty_period_segments dps ON dps.segment_id = s.id
		WHERE dps.duty_period_id = ?
		ORDER BY dps.seq ASC`, dutyID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.FlightSegment
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// scanDuty scans one duty_periods row (no segments loaded).
func scanDuty(s scanner) (*domain.DutyPeriod, error) {
	var d domain.DutyPeriod
	var aug, owe int
	var report, release string
	if err := s.Scan(&d.ID, &d.CrewID, &d.TripID, &report, &release, &aug, &d.SplitBreakMin,
		&d.Status, &d.UnforeseenExtensionMin, &owe); err != nil {
		return nil, mapErr(err)
	}
	d.IsAugmented = aug != 0
	d.OweAugmentedRest = owe != 0
	if report != "" {
		t, err := time.Parse(time.RFC3339Nano, report)
		if err == nil {
			d.ReportTime = t.UTC()
		}
	}
	if release != "" {
		t, err := time.Parse(time.RFC3339Nano, release)
		if err == nil {
			d.ReleaseTime = t.UTC()
		}
	}
	return &d, nil
}
