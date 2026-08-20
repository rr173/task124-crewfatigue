package store

import (
	"context"
	"time"

	"task124-crewfatigue/internal/domain"
)

// PutSnapshot upserts a cumulative snapshot row for a (crew, window) pair.
// Snapshots are derived; the authoritative source is the event log, and
// ReplayForCrew overwrites snapshots that disagree with the replay.
func PutSnapshot(ctx context.Context, tx DBTX, crewID int64, windowKind string, asOf time.Time, cumulativeMin int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cumulative_snapshots (crew_id, window_kind, as_of, cumulative_min)
		VALUES (?,?,?,?) ON CONFLICT(crew_id, window_kind) DO UPDATE SET as_of=excluded.as_of, cumulative_min=excluded.cumulative_min`,
		crewID, windowKind, asOf.UTC().Format(time.RFC3339Nano), cumulativeMin)
	return mapErr(err)
}

// GetSnapshot loads one cumulative snapshot. Returns ErrCrewNotFound-shaped
// zero when absent (callers treat as "no persisted snapshot yet").
func GetSnapshot(ctx context.Context, tx DBTX, crewID int64, windowKind string) (asOf time.Time, cumulativeMin int, ok bool, err error) {
	row := tx.QueryRowContext(ctx, `SELECT as_of, cumulative_min FROM cumulative_snapshots WHERE crew_id = ? AND window_kind = ?`,
		crewID, windowKind)
	var asOfStr string
	if err = row.Scan(&asOfStr, &cumulativeMin); err != nil {
		return time.Time{}, 0, false, nil
	}
	if asOfStr != "" {
		t, perr := time.Parse(time.RFC3339Nano, asOfStr)
		if perr == nil {
			asOf = t.UTC()
		}
	}
	return asOf, cumulativeMin, true, nil
}

// AllSnapshots returns all cumulative snapshots for a crew member. Used by the
// replay-to-verify path to compare stored snapshots against the rebuilt truth.
func AllSnapshots(ctx context.Context, tx DBTX, crewID int64) (map[string]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT window_kind, cumulative_min FROM cumulative_snapshots WHERE crew_id = ?`, crewID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, mapErr(err)
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// SnapshotWindowDomain exposes the locked window kind constants from the store
// package's perspective for the compliance layer to iterate deterministically.
func SnapshotWindowKinds() []string {
	return []string{WindowKind28d, WindowKind168h, WindowKind365d}
}

// SnapshotAllCumulative is a convenience aggregate used by the cumulative API.
type SnapshotAllCumulative struct {
	CrewID      int64
	AsOf        time.Time
	Used28dMin  int
	Used168hMin int
	Used365dMin int
}

// LoadAllCumulative loads the three window snapshots at once.
func LoadAllCumulative(ctx context.Context, tx DBTX, crewID int64) (*SnapshotAllCumulative, error) {
	out := &SnapshotAllCumulative{CrewID: crewID, AsOf: nowUTC()}
	for _, k := range SnapshotWindowKinds() {
		_, n, _, err := GetSnapshot(ctx, tx, crewID, k)
		if err != nil {
			return nil, err
		}
		switch k {
		case WindowKind28d:
			out.Used28dMin = n
		case WindowKind168h:
			out.Used168hMin = n
		case WindowKind365d:
			out.Used365dMin = n
		}
	}
	return out, nil
}

var _ = domain.EventEvaluation // keep domain import for documentation anchor
