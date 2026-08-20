package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// CreateEvaluation persists a legality evaluation result. TripID may be 0 for
// evaluations of proposed (non-persisted) trips; a 0 is stored as NULL.
func CreateEvaluation(ctx context.Context, tx DBTX, e *domain.LegalityEvaluation) (int64, error) {
	if e.CrewID <= 0 {
		return 0, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if e.EvaluatedAt.IsZero() {
		e.EvaluatedAt = nowUTC()
	}
	vj, _ := json.Marshal(e.Violations)
	mj, _ := json.Marshal(e.Metrics)
	var tripArg any
	if e.TripID > 0 {
		tripArg = e.TripID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO legality_evaluations
		(crew_id, trip_id, evaluated_at, verdict, violations_json, metrics_json)
		VALUES (?,?,?,?,?,?)`,
		e.CrewID, tripArg, e.EvaluatedAt.UTC().Format(time.RFC3339Nano),
		string(e.Verdict), string(vj), string(mj))
	if err != nil {
		return 0, mapErr(err)
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return id, nil
}

// GetEvaluation loads an evaluation by ID.
func GetEvaluation(ctx context.Context, tx DBTX, id int64) (*domain.LegalityEvaluation, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, crew_id, trip_id, evaluated_at, verdict, violations_json, metrics_json
		FROM legality_evaluations WHERE id = ?`, id)
	return scanEvaluation(row)
}

// GetEvaluationByTrip loads the most recent evaluation for a (crew, trip).
func GetEvaluationByTrip(ctx context.Context, tx DBTX, crewID, tripID int64) (*domain.LegalityEvaluation, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, crew_id, trip_id, evaluated_at, verdict, violations_json, metrics_json
		FROM legality_evaluations WHERE crew_id = ? AND trip_id = ? ORDER BY evaluated_at DESC, id DESC LIMIT 1`,
		crewID, tripID)
	return scanEvaluation(row)
}

// scanEvaluation scans one legality_evaluations row.
func scanEvaluation(s scanner) (*domain.LegalityEvaluation, error) {
	var e domain.LegalityEvaluation
	var evaluated, vj, mj string
	if err := s.Scan(&e.ID, &e.CrewID, &e.TripID, &evaluated, &e.Verdict, &vj, &mj); err != nil {
		return nil, mapErr(err)
	}
	if evaluated != "" {
		t, err := time.Parse(time.RFC3339Nano, evaluated)
		if err == nil {
			e.EvaluatedAt = t.UTC()
		}
	}
	_ = json.Unmarshal([]byte(vj), &e.Violations)
	_ = json.Unmarshal([]byte(mj), &e.Metrics)
	return &e, nil
}
