package compliance

import (
	"context"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

// rebuildSegmentsInWindow replays SEGMENT_LANDED events and returns the
// segments whose block time counts toward the rolling window ending at asOf.
// A segment counts toward a window when its event ts (the actual departure)
// falls in (asOf - window, asOf]. The block time is attributed wholly to the
// window of its departure, which is the locked deterministic attribution rule.
func rebuildSegmentsInWindow(events []*domain.ComplianceEvent, asOf time.Time, window time.Duration) []domain.FlightSegment {
	from := asOf.Add(-window)
	var out []domain.FlightSegment
	for _, e := range events {
		if e.Kind != domain.EventSegmentLanded {
			continue
		}
		if e.Ts.Before(from) || e.Ts.After(asOf) {
			continue
		}
		p, ok := store.DecodePayload(e).(*store.EventPayloadSegment)
		if !ok || p == nil {
			continue
		}
		out = append(out, domain.FlightSegment{
			ID: p.SegmentID, TripID: p.TripID, BlockTimeMin: p.BlockTimeMin,
			ScheduledDep: e.Ts.UTC(),
		})
	}
	return out
}

// rebuildDutiesInWindow replays DUTY_CLOSED events and returns duty periods
// whose REPORT time falls in (asOf - window, asOf]. R5 windows by report_time.
// Each returned duty carries its full FDP (release - report), reconstructed
// from the DUTY_CLOSED payload's FDPMin and the event ts (release time).
func rebuildDutiesInWindow(events []*domain.ComplianceEvent, asOf time.Time, window time.Duration) []domain.DutyPeriod {
	from := asOf.Add(-window)
	var out []domain.DutyPeriod
	for _, e := range events {
		if e.Kind != domain.EventDutyClosed {
			continue
		}
		p, ok := store.DecodePayload(e).(*store.EventPayloadDuty)
		if !ok || p == nil {
			continue
		}
		// Reconstruct report = release - FDP; release = event ts.
		release := e.Ts
		report := release.Add(-time.Duration(p.FDPMin) * time.Minute)
		if !report.After(from) || report.After(asOf) {
			continue
		}
		out = append(out, domain.DutyPeriod{ID: p.DutyID, ReportTime: report, ReleaseTime: release})
	}
	return out
}

// lastDutyReleaseBefore returns the release time of the most recent
// DUTY_CLOSED event whose ts is before ref. Returns zero if none.
func lastDutyReleaseBefore(events []*domain.ComplianceEvent, ref time.Time) time.Time {
	var last time.Time
	for _, e := range events {
		if e.Kind != domain.EventDutyClosed {
			continue
		}
		if !e.Ts.Before(ref) {
			continue
		}
		if e.Ts.After(last) {
			last = e.Ts
		}
	}
	return last
}

// computeCompensatoryDebt replays DUTY_CLOSED events (in report-time order) to
// rebuild the outstanding compensatory-rest deficit at asOf. A "reduced rest"
// is the gap between the previous duty's release and this duty's report that
// falls in [MinRestReducedHours, MinRestHours) — it is allowed once but creates
// a deficit of (MinRest - gap) that must be made up by a COMPENSATORY
// REST_COMPLETED event within CompensatoryWindowHours. A compensatory rest
// registered after a deficit's deadline cannot retroactively satisfy it: only
// deficits whose make-up window is still open at the rest's completion time
// are credited, so a late rest leaves the deficit outstanding. A subsequent
// reduced rest while a deficit is outstanding is the R7 violation the
// evaluator flags.
//
// Returns the remaining deficit (minutes) and the earliest outstanding
// deadline (nil if none).
func computeCompensatoryDebt(events []*domain.ComplianceEvent, asOf time.Time) (int, *time.Time) {
	type closed struct {
		report  time.Time
		release time.Time
	}
	// Collect closed duty periods from DUTY_CLOSED events (release = event ts,
	// report = release - FDP).
	var duties []closed
	for _, e := range events {
		if e.Kind != domain.EventDutyClosed {
			continue
		}
		p, ok := store.DecodePayload(e).(*store.EventPayloadDuty)
		if !ok || p == nil {
			continue
		}
		release := e.Ts
		report := release.Add(-time.Duration(p.FDPMin) * time.Minute)
		duties = append(duties, closed{report: report, release: release})
	}
	// Sort by report time (stable: insertion sort; small N per crew).
	for i := 1; i < len(duties); i++ {
		for j := i; j > 0 && duties[j].report.Before(duties[j-1].report); j-- {
			duties[j], duties[j-1] = duties[j-1], duties[j]
		}
	}

	type deficit struct {
		amount   int
		deadline time.Time
	}
	var open []deficit
	minRest := domain.MinRestHours * 60
	reducedFloor := domain.MinRestReducedHours * 60
	var prevRelease time.Time
	// Walk duties and rests interleaved by time. Build a merged stream keyed by
	// the event ts (rest end / duty release) so compensatory rests clear the
	// deficits in the order they were made up.
	type streamEv struct {
		ts       time.Time
		isRest   bool
		credit   int // compensatory minutes (rest only)
		gap      int // reduced gap (duty only)
	}
	stream := make([]streamEv, 0, len(duties)+len(events))
	for _, d := range duties {
		gap := 0
		if !prevRelease.IsZero() {
			g := int(d.report.Sub(prevRelease).Minutes())
			if g < reducedFloor {
				gap = 0 // below floor: handled as a hard violation by the evaluator
			} else if g < minRest {
				gap = g
			}
		}
		stream = append(stream, streamEv{ts: d.report, isRest: false, gap: gap})
		prevRelease = d.release
	}
	for _, e := range events {
		if e.Kind != domain.EventRestCompleted {
			continue
		}
		p, ok := store.DecodePayload(e).(*store.EventPayloadRest)
		if !ok || p == nil {
			continue
		}
		if p.RestType != string(domain.RestCompensatory) {
			continue
		}
		stream = append(stream, streamEv{ts: e.Ts, isRest: true, credit: p.DurationMin})
	}
	// Stable sort by ts.
	for i := 1; i < len(stream); i++ {
		for j := i; j > 0 && stream[j].ts.Before(stream[j-1].ts); j-- {
			stream[j], stream[j-1] = stream[j-1], stream[j]
		}
	}
	for _, ev := range stream {
		if ev.isRest {
			credit := ev.credit
			for i := range open {
				if credit <= 0 {
					break
				}
				// A compensatory rest registered (completed) after a deficit's
				// 72h deadline cannot retroactively satisfy it: only deficits
				// whose make-up window is still open at the rest's completion
				// time are credited. A late rest leaves the deficit outstanding.
				if ev.ts.After(open[i].deadline) {
					continue
				}
				take := open[i].amount
				if take > credit {
					take = credit
				}
				open[i].amount -= take
				credit -= take
			}
			pruned := open[:0]
			for _, d := range open {
				if d.amount > 0 {
					pruned = append(pruned, d)
				}
			}
			open = pruned
		} else if ev.gap > 0 {
			open = append(open, deficit{
				amount:   minRest - ev.gap,
				deadline: ev.ts.Add(domain.CompensatoryWindowHours * time.Hour),
			})
		}
	}
	total := 0
	var deadline *time.Time
	for _, d := range open {
		if d.amount <= 0 {
			continue
		}
		total += d.amount
		if deadline == nil || d.deadline.Before(*deadline) {
			db := d.deadline
			deadline = &db
		}
	}
	return total, deadline
}

// computeEarlyStreakFromDuties replays DUTY_CLOSED events to determine the
// consecutive early-start streak carried into ref (the proposed duty's
// report). A duty is "early" if its report time is before 07:00 in the crew's
// home timezone. The streak resets on a non-early duty or a rest of >=36h
// ending before ref.
//
// It merges the closed-duty report times and rest summaries into one
// chronological stream and walks it. This helper takes the precomputed
// closed-duty reports (in order) and rest durations.
func computeEarlyStreakFromDuties(reports []time.Time, rests []restEntry, tz string, ref time.Time) int {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	stream := make([]earlyStreamEv, 0, len(reports)+len(rests))
	for _, r := range reports {
		if !r.Before(ref) {
			continue
		}
		stream = append(stream, earlyStreamEv{t: r, early: r.In(loc).Hour() < domain.EarlyStartLocalHour})
	}
	for _, rs := range rests {
		if !rs.end.Before(ref) {
			continue
		}
		stream = append(stream, earlyStreamEv{t: rs.end, isRest: true, restH: rs.hours})
	}
	sortByTime(stream)
	streak := 0
	for _, e := range stream {
		if e.isRest && e.restH >= domain.EarlyStartResetRestHours {
			streak = 0
			continue
		}
		if e.isRest {
			continue
		}
		if e.early {
			streak++
		} else {
			streak = 0
		}
	}
	return streak
}

// restEntry is a compact rest summary for the early-streak computation.
type restEntry struct {
	end   time.Time
	hours int
}

// hasWeeklyRestInRests reports whether the trailing 168h window ending at asOf
// contains a qualifying weekly rest (>=30h, or >=25h when covers_weekly).
func hasWeeklyRestInRests(rests []domain.RestPeriod, asOf time.Time) bool {
	windowStart := asOf.Add(-domain.Window168h)
	for i := range rests {
		r := rests[i]
		if !r.End.After(windowStart) {
			continue
		}
		if r.End.After(asOf) {
			continue
		}
		durHours := int(r.Duration().Hours())
		needed := domain.WeeklyRestHours
		if r.CoversWeekly {
			needed = domain.WeeklyRestHomeBaseHours
		}
		if durHours >= needed {
			return true
		}
	}
	return false
}

// toDomainRests converts []*RestPeriod to a value slice.
func toDomainRests(in []*domain.RestPeriod) []domain.RestPeriod {
	out := make([]domain.RestPeriod, len(in))
	for i, r := range in {
		out[i] = *r
	}
	return out
}

// sumBlock sums the block_time_min of the given segments.
func sumBlock(segs []domain.FlightSegment) int {
	var n int
	for i := range segs {
		n += segs[i].BlockTimeMin
	}
	return n
}

// sumDutyFDP sums the FDP of the given duties.
func sumDutyFDP(duties []domain.DutyPeriod) int {
	var n int
	for i := range duties {
		n += int(duties[i].FDP().Minutes())
	}
	return n
}

// sortByTime sorts the event stream chronologically (stable for equal ts).
func sortByTime(s []earlyStreamEv) {
	// Insertion sort is fine; the stream is small (per-crew history).
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].t.Before(s[j-1].t); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// earlyStreamEv is the concrete event type used by computeEarlyStreakFromDuties.
type earlyStreamEv struct {
	t      time.Time
	early  bool
	isRest bool
	restH  int
}

// sortByTimeDispatch is a thin dispatcher kept so callers can pass either the
// private earlyStreamEv slice. It is intentionally minimal.
func sortByTimeDispatch(s []earlyStreamEv) { sortByTime(s) }

// computeEarlyStreak reads the crew's closed-duty reports and completed rests
// from the store (inside tx) and delegates to computeEarlyStreakFromDuties.
// It is the tx-aware entry point used by EvaluateTrip and RestDebt.
func computeEarlyStreak(ctx context.Context, tx store.DBTX, crewID int64, ref time.Time) int {
	crew, err := store.GetCrew(ctx, tx, crewID)
	if err != nil {
		return 0
	}
	duties, err := store.ListDutyPeriods(ctx, tx, crewID, string(domain.DutyClosed))
	if err != nil {
		return 0
	}
	reports := make([]time.Time, 0, len(duties))
	for _, d := range duties {
		reports = append(reports, d.ReportTime)
	}
	restRows, err := store.ListRest(ctx, tx, crewID)
	if err != nil {
		return 0
	}
	rests := make([]restEntry, 0, len(restRows))
	for _, r := range restRows {
		rests = append(rests, restEntry{end: r.End, hours: int(r.Duration().Hours())})
	}
	return computeEarlyStreakFromDuties(reports, rests, crew.HomeTZ, ref)
}
