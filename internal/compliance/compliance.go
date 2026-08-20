// Package compliance is the coordinator: it rebuilds cumulative state from
// the compliance-event log (the restart recovery path), assembles the inputs
// the fatigue rules need, runs an evaluation, persists the result and reports
// rest debt. Replay is authoritative: any persisted snapshot that disagrees
// with the replay is overwritten and a REPLAY_OVERRIDE event is recorded.
package compliance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/fatigue"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/store"
)

// Service wires the store, schedule service and crew lookup into the
// compliance engine.
type Service struct {
	st *store.Store
	sch *schedule.Service
}

// New returns a compliance service.
func New(st *store.Store, sch *schedule.Service) *Service {
	return &Service{st: st, sch: sch}
}

// ReplayForCrew rebuilds the rolling-window cumulative counters for a crew
// member from the compliance-event log, compares them to the persisted
// snapshots, and overwrites any snapshot that disagrees. Returns the number
// of snapshots overwritten. This is the restart recovery entry point and is
// also exposed via POST /admin/replay for ad-hoc consistency checks.
func (s *Service) ReplayForCrew(ctx context.Context, crewID int64, asOf time.Time) (overrides int, snap *store.SnapshotAllCumulative, err error) {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	err = s.st.InTx(ctx, func(tx store.DBTX) error {
		events, rerr := store.ListEventsForCrew(ctx, tx, crewID)
		if rerr != nil {
			return rerr
		}
		// Rebuild the three windows by replaying segment/duty events.
		seg28 := rebuildSegmentsInWindow(events, asOf, domain.Window28d)
		seg365 := rebuildSegmentsInWindow(events, asOf, domain.Window365d)
		dut168 := rebuildDutiesInWindow(events, asOf, domain.Window168h)

		used28 := sumBlock(seg28)
		used168 := sumDutyFDP(dut168)
		used365 := sumBlock(seg365)

		// Compare + overwrite snapshots.
		wm := map[string]int{
			store.WindowKind28d:  used28,
			store.WindowKind168h: used168,
			store.WindowKind365d: used365,
		}
		for kind, val := range wm {
			_, stored, _, gerr := store.GetSnapshot(ctx, tx, crewID, kind)
			if gerr != nil {
				return gerr
			}
			if stored != val {
				overrides++
				if err := store.PutSnapshot(ctx, tx, crewID, kind, asOf, val); err != nil {
					return err
				}
				pj, _ := json.Marshal(map[string]any{"window": kind, "stored": stored, "rebuilt": val})
				ev := &domain.ComplianceEvent{
					CrewID: crewID, Ts: asOf.UTC(), Kind: domain.EventReplayOverride, PayloadJSON: string(pj),
				}
				if _, err := store.AppendEvent(ctx, tx, ev); err != nil {
					return err
				}
			}
		}
		snap = &store.SnapshotAllCumulative{
			CrewID: crewID, AsOf: asOf,
			Used28dMin: used28, Used168hMin: used168, Used365dMin: used365,
		}
		return nil
	})
	return overrides, snap, err
}

// Cumulative returns the live rolling-window usage for a crew member at asOf,
// computed from the event log (replay) rather than the cached snapshot, so
// the API always reflects the authoritative state.
func (s *Service) Cumulative(ctx context.Context, crewID int64, asOf time.Time) (*store.SnapshotAllCumulative, error) {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	var snap *store.SnapshotAllCumulative
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		events, rerr := store.ListEventsForCrew(ctx, tx, crewID)
		if rerr != nil {
			return rerr
		}
		snap = &store.SnapshotAllCumulative{CrewID: crewID, AsOf: asOf}
		snap.Used28dMin = sumBlock(rebuildSegmentsInWindow(events, asOf, domain.Window28d))
		snap.Used168hMin = sumDutyFDP(rebuildDutiesInWindow(events, asOf, domain.Window168h))
		snap.Used365dMin = sumBlock(rebuildSegmentsInWindow(events, asOf, domain.Window365d))
		return nil
	})
	return snap, err
}

// EvaluateTrip evaluates a proposed trip (a sequence of planned segments) at
// asOf against all rules, persists the result, and returns it. The proposed
// segments are NOT required to be persisted; this is the pre-flight legality
// check.
func (s *Service) EvaluateTrip(ctx context.Context, req domain.EvaluateTripRequest, asOf time.Time) (*domain.LegalityEvaluation, error) {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	if req.CrewID <= 0 {
		return nil, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	if len(req.Segments) == 0 {
		return nil, domain.ErrDutyEmpty
	}
	// Sort segments by scheduled_dep to compute report/release deterministically.
	segs := append([]domain.SegmentInput(nil), req.Segments...)
	sort.Slice(segs, func(i, j int) bool { return segs[i].ScheduledDep.Before(segs[j].ScheduledDep) })
	report := segs[0].ScheduledDep.Add(-domain.ReportLeadMin * time.Minute)
	release := segs[len(segs)-1].ScheduledArr.Add(domain.ReleaseLagMin * time.Minute)

	var eval *domain.LegalityEvaluation
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		crew, err := store.GetCrew(ctx, tx, req.CrewID)
		if err != nil {
			return err
		}
		ac, err := store.GetAircraft(ctx, tx, req.AircraftType)
		if err != nil {
			return err
		}

		// Assemble the fatigue inputs from persisted history + the proposed duty.
		in := fatigue.EvalInput{
			T:    asOf,
			Crew: crew,
			Proposed: fatigue.ProposedDuty{
				ReportTime:       report,
				ReleaseTime:      release,
				SegmentCount:     len(segs),
				IsAugmented:      req.IsAugmented,
				SplitBreakMin:    req.SplitBreakMin,
				AircraftFacility: ac.RestFacilityClass,
				ApplyUnforeseen:  false,
			},
		}
		// Historical windows.
		events, err := store.ListEventsForCrew(ctx, tx, req.CrewID)
		if err != nil {
			return err
		}
		in.LandedIn28d = rebuildSegmentsInWindow(events, asOf, domain.Window28d)
		in.LandedIn365d = rebuildSegmentsInWindow(events, asOf, domain.Window365d)
		in.ClosedIn168h = rebuildDutiesInWindow(events, asOf, domain.Window168h)

		// R7: last closed duty's release time + last rest before report.
		if prev := lastDutyReleaseBefore(events, report); !prev.IsZero() {
			in.PrevRelease = prev
		}
		if r, _ := store.LastRestBefore(ctx, tx, req.CrewID, report); r != nil {
			in.LastRestBeforeT = r
		}
		rests168, _ := store.ListRestCompletedBetween(ctx, tx, req.CrewID, asOf.Add(-domain.Window168h), asOf)
		in.RestsIn168h = toDomainRests(rests168)

		// R9: early-start streak carried from history up to this proposed duty.
		in.EarlyStartStreak = computeEarlyStreak(ctx, tx, req.CrewID, report)

		// R10: unforeseen count this year.
		used, _ := store.CountEventsKindYear(ctx, tx, req.CrewID, domain.EventUnforeseenExtended, report)
		in.UnforeseenUsedYear = used

		// R7 compensatory debt: any reduced rest not yet made up.
		compMin, dueBy := computeCompensatoryDebt(events, asOf)
		in.CompensatoryOwedMin = compMin
		if dueBy != nil {
			in.CompensatoryDueBy = *dueBy
		}

		result := fatigue.Eval(in)
		le := &domain.LegalityEvaluation{
			CrewID: req.CrewID, TripID: 0, EvaluatedAt: asOf,
			Verdict: result.Verdict, Violations: result.Violations, Metrics: result.Metrics,
		}
		id, err := store.CreateEvaluation(ctx, tx, le)
		if err != nil {
			return err
		}
		le.ID = id
		eval = le
		// Record an EVALUATION event.
		pj, _ := json.Marshal(map[string]any{"verdict": string(le.Verdict), "violations": len(le.Violations)})
		ev := &domain.ComplianceEvent{
			CrewID: req.CrewID, Ts: asOf.UTC(), Kind: domain.EventEvaluation, PayloadJSON: string(pj),
		}
		_, _ = store.AppendEvent(ctx, tx, ev)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return eval, nil
}

// EvaluatePersistedTrip evaluates the segments of an existing trip (read from
// the store) and persists the result linked to that trip.
func (s *Service) EvaluatePersistedTrip(ctx context.Context, crewID, tripID int64, isAugmented bool, splitBreakMin int, applyUnforeseen bool, asOf time.Time) (*domain.LegalityEvaluation, error) {
	var segs []*domain.FlightSegment
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		var gerr error
		segs, gerr = store.ListSegmentsByTrip(ctx, tx, tripID)
		if gerr != nil {
			return gerr
		}
		if len(segs) == 0 {
			return domain.ErrDutyEmpty
		}
		// Enforce the single-facility-class invariant on persisted segments.
		// Legacy rows inserted directly (bypassing schedule.AddSegment) could
		// otherwise be evaluated under the first segment's aircraft type
		// silently. Reject the whole trip instead of guessing which class
		// governs the (R2) augmented extension.
		classes, gerr := store.DistinctTripFacilityClasses(ctx, tx, tripID)
		if gerr != nil {
			return gerr
		}
		if len(classes) > 1 {
			return fmt.Errorf("%w: trip %d mixes rest facility classes %v",
				domain.ErrInvariantViolation, tripID, classes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	in := domain.EvaluateTripRequest{
		CrewID: crewID, AircraftType: segs[0].AircraftType,
		IsAugmented: isAugmented, SplitBreakMin: splitBreakMin,
	}
	for _, sg := range segs {
		in.Segments = append(in.Segments, domain.SegmentInput{
			DepAirport: sg.DepAirport, ArrAirport: sg.ArrAirport,
			ScheduledDep: sg.ScheduledDep, ScheduledArr: sg.ScheduledArr,
		})
	}
	// Evaluate, then re-tag the persisted evaluation with the real trip_id.
	eval, err := s.EvaluateTrip(ctx, in, asOf)
	if err != nil {
		return nil, err
	}
	err = s.st.InTx(ctx, func(tx store.DBTX) error {
		_, e := tx.ExecContext(ctx, `UPDATE legality_evaluations SET trip_id=? WHERE id=?`, tripID, eval.ID)
		return e
	})
	if err != nil {
		return nil, err
	}
	eval.TripID = tripID
	// Apply unforeseen flag retroactively if requested and the evaluation was
	// otherwise legal (the R10 check ran with ApplyUnforeseen=false above; we
	// re-check here against the yearly quota to mirror the close path).
	if applyUnforeseen {
		_ = s.recheckUnforeseen(ctx, crewID, eval, asOf)
	}
	return eval, nil
}

// recheckUnforeseen re-runs the R10 check with ApplyUnforeseen=true and, if a
// violation arises, updates the persisted verdict to ILLEGAL with the added
// violation. Kept simple: only adds the violation if the quota is exceeded.
func (s *Service) recheckUnforeseen(ctx context.Context, crewID int64, eval *domain.LegalityEvaluation, asOf time.Time) error {
	return s.st.InTx(ctx, func(tx store.DBTX) error {
		used, _ := store.CountEventsKindYear(ctx, tx, crewID, domain.EventUnforeseenExtended, asOf)
		if used >= domain.UnforeseenYearlyLimit {
			eval.Violations = append(eval.Violations, domain.Violation{
				Rule: domain.RuleUnforeseenExtend,
				Message: fmt.Sprintf("unforeseen extension #%d exceeds yearly limit %d", used+1, domain.UnforeseenYearlyLimit),
				Actual: fmt.Sprintf("%d", used+1), Limit: fmt.Sprintf("%d", domain.UnforeseenYearlyLimit),
			})
			eval.Verdict = domain.VerdictIllegal
			eval.Metrics.UnforeseenUsedYear = used
			eval.Metrics.UnforeseenLimitYear = domain.UnforeseenYearlyLimit
		}
		vj, _ := json.Marshal(eval.Violations)
		_, err := tx.ExecContext(ctx, `UPDATE legality_evaluations SET verdict=?, violations_json=? WHERE id=?`,
			string(eval.Verdict), string(vj), eval.ID)
		return err
	})
}

// GetEvaluation loads an evaluation by ID.
func (s *Service) GetEvaluation(ctx context.Context, id int64) (*domain.LegalityEvaluation, error) {
	var e *domain.LegalityEvaluation
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetEvaluation(ctx, tx, id)
		if err != nil {
			return err
		}
		e = got
		return nil
	})
	return e, err
}

// GetEvaluationByTrip loads the latest evaluation for a (crew, trip).
func (s *Service) GetEvaluationByTrip(ctx context.Context, crewID, tripID int64) (*domain.LegalityEvaluation, error) {
	var e *domain.LegalityEvaluation
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetEvaluationByTrip(ctx, tx, crewID, tripID)
		if err != nil {
			return err
		}
		e = got
		return nil
	})
	return e, err
}

// RestDebt computes a crew member's outstanding rest obligations at asOf:
// compensatory deficit (R7), weekly rest owed (R8), augmented rest owed (R10),
// early-start streak (R9) and unforeseen count this year (R10).
func (s *Service) RestDebt(ctx context.Context, crewID int64, asOf time.Time) (*domain.RestDebt, error) {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	var debt *domain.RestDebt
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		events, rerr := store.ListEventsForCrew(ctx, tx, crewID)
		if rerr != nil {
			return rerr
		}
		compMin, dueBy := computeCompensatoryDebt(events, asOf)
		rests168, _ := store.ListRestCompletedBetween(ctx, tx, crewID, asOf.Add(-domain.Window168h), asOf)
		owesWeekly := !hasWeeklyRestInRests(toDomainRests(rests168), asOf)
		streak := computeEarlyStreak(ctx, tx, crewID, asOf)
		used, _ := store.CountEventsKindYear(ctx, tx, crewID, domain.EventUnforeseenExtended, asOf)
		// Augmented-rest owed: true if any DUTY_CLOSED event carried owe=true.
		oweAug := false
		for _, e := range events {
			if e.Kind == domain.EventDutyClosed {
				if p, ok := store.DecodePayload(e).(*store.EventPayloadDuty); ok && p != nil && p.OweAugmentedRest {
					// Cleared only by a later AUGMENTED rest.
					oweAug = true
				}
			}
			if e.Kind == domain.EventRestCompleted {
				if p, ok := store.DecodePayload(e).(*store.EventPayloadRest); ok && p != nil && p.RestType == string(domain.RestAugmented) {
					oweAug = false
				}
			}
		}
		debt = &domain.RestDebt{
			CrewID: crewID, AsOf: asOf,
			CompensatoryOwedMin: compMin, CompensatoryDueBy: dueBy,
			OwesWeeklyRest: owesWeekly, OwesAugmentedRest: oweAug,
			EarlyStartStreak: streak, UnforeseenUsedYear: used,
		}
		return nil
	})
	return debt, err
}

// CompensatoryRestCheck verifies whether a proposed compensatory rest would
// clear an outstanding reduced-rest deficit (R7). Returns true if the deficit
// is zero or the provided rest clears it.
func (s *Service) CompensatoryRestCheck(ctx context.Context, crewID int64, proposedRestMin int, asOf time.Time) (ok bool, remaining int, err error) {
	debt, err := s.RestDebt(ctx, crewID, asOf)
	if err != nil {
		return false, 0, err
	}
	if debt.CompensatoryOwedMin <= 0 {
		return true, 0, nil
	}
	remaining = debt.CompensatoryOwedMin - proposedRestMin
	if remaining < 0 {
		remaining = 0
	}
	return remaining == 0, remaining, nil
}
