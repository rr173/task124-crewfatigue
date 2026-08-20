package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// AppendEvent appends a compliance event to the append-only log. This log is
// the single source of truth for rebuilding cumulative state on restart.
func AppendEvent(ctx context.Context, tx DBTX, e *domain.ComplianceEvent) (int64, error) {
	if e.CrewID <= 0 {
		return 0, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if e.Ts.IsZero() {
		e.Ts = nowUTC()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO compliance_events (crew_id, ts, kind, payload_json) VALUES (?,?,?,?)`,
		e.CrewID, e.Ts.UTC().Format(time.RFC3339Nano), string(e.Kind), e.PayloadJSON)
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return id, nil
}

// ListEventsForCrew returns all compliance events for a crew member ordered by
// ts then id (stable order for deterministic replay).
func ListEventsForCrew(ctx context.Context, tx DBTX, crewID int64) ([]*domain.ComplianceEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, crew_id, ts, kind, payload_json FROM compliance_events
		WHERE crew_id = ? ORDER BY ts ASC, id ASC`, crewID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.ComplianceEvent
	for rows.Next() {
		var e domain.ComplianceEvent
		var ts string
		if err := rows.Scan(&e.ID, &e.CrewID, &ts, &e.Kind, &e.PayloadJSON); err != nil {
			return nil, mapErr(err)
		}
		if ts != "" {
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err == nil {
				e.Ts = t.UTC()
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// CountEventsKindYear counts compliance events of the given kind whose ts falls
// in the calendar year (UTC) of ref. Used for the R10 unforeseen yearly limit.
func CountEventsKindYear(ctx context.Context, tx DBTX, crewID int64, kind domain.EventKind, ref time.Time) (int, error) {
	year := ref.UTC().Year()
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	row := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM compliance_events
		WHERE crew_id = ? AND kind = ? AND ts >= ? AND ts < ?`,
		crewID, string(kind), from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano))
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// EventPayload is the decoded payload of a SEGMENT_LANDED event.
type EventPayloadSegment struct {
	SegmentID     int64  `json:"segment_id"`
	BlockTimeMin  int    `json:"block_time_min"`
	TripID        int64  `json:"trip_id"`
	DepartureTime string `json:"departure_time,omitempty"`
}

// EventPayloadDuty is the decoded payload of a DUTY_CLOSED event.
type EventPayloadDuty struct {
	DutyID                 int64 `json:"duty_id"`
	FDPMin                 int   `json:"fdp_min"`
	UnforeseenExtensionMin int   `json:"unforeseen_extension_min"`
	OweAugmentedRest       bool  `json:"owe_augmented_rest"`
}

// EventPayloadRest is the decoded payload of a REST_COMPLETED event.
type EventPayloadRest struct {
	RestID         int64  `json:"rest_id"`
	RestType       string `json:"rest_type"`
	DurationMin    int    `json:"duration_min"`
	CoversWeekly   bool   `json:"covers_weekly"`
	CompensatesMin int    `json:"compensates_min"`
}

// EventPayloadUnforeseen is the decoded payload of an UNFORESEEN_EXTENSION event.
type EventPayloadUnforeseen struct {
	DutyID   int64 `json:"duty_id"`
	AddedMin int   `json:"added_min"`
}

// DecodePayload unmarshals an event payload JSON into a freshly allocated value
// of the struct type appropriate for the event kind. Returns nil for kinds with
// no payload.
func DecodePayload(e *domain.ComplianceEvent) any {
	if e.PayloadJSON == "" {
		return nil
	}
	switch e.Kind {
	case domain.EventSegmentLanded:
		var p EventPayloadSegment
		_ = json.Unmarshal([]byte(e.PayloadJSON), &p)
		return &p
	case domain.EventDutyClosed:
		var p EventPayloadDuty
		_ = json.Unmarshal([]byte(e.PayloadJSON), &p)
		return &p
	case domain.EventRestCompleted:
		var p EventPayloadRest
		_ = json.Unmarshal([]byte(e.PayloadJSON), &p)
		return &p
	case domain.EventUnforeseenExtended:
		var p EventPayloadUnforeseen
		_ = json.Unmarshal([]byte(e.PayloadJSON), &p)
		return &p
	}
	return nil
}
