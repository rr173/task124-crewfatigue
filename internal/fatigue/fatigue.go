// Package fatigue implements the locked fatigue-rule set (§7 of the design
// doc) as pure functions. Inputs are time-bounded aggregates of landed
// segments, closed duty periods and completed rests plus the proposed trip
// being evaluated; outputs are numeric metrics and a list of rule violations.
// No storage, no clock side effects: the compliance package feeds
// fully-resolved inputs and persists the result.
package fatigue

import (
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
)

// EvalInput is the fully-resolved input to an evaluation at instant T.
type EvalInput struct {
	// T is the evaluation instant (UTC). All rolling windows are relative to T.
	T time.Time
	// Crew is the crew member under evaluation (HomeTZ drives local-hour
	// classification for FDP band and early-start detection).
	Crew *domain.CrewMember

	// Proposed is the trip being evaluated: its planned report/release times,
	// segment count, augmented flag and split break.
	Proposed ProposedDuty

	// LandedIn28d are landed segments (actual_dep in window) used for R4/R6.
	// The compliance layer has already windowed them; here they are summed.
	LandedIn28d  []domain.FlightSegment
	LandedIn365d []domain.FlightSegment

	// ClosedIn168h are closed duty periods (report_time in window) used for R5.
	ClosedIn168h []domain.DutyPeriod

	// PrevRelease is the release time of the last closed duty period before
	// this proposed duty (zero if none). Used by R7 min-rest.
	PrevRelease time.Time

	// LastRestBeforeT is the most recent completed rest ending at or before T.
	// Used by R7 to measure rest since the last duty and by R8 weekly coverage.
	LastRestBeforeT *domain.RestPeriod

	// RestsIn168h are completed rests whose end is within (T-168h, T].
	// Used by R8 weekly coverage.
	RestsIn168h []domain.RestPeriod

	// EarlyStartStreak is the consecutive early-start count carried in from
	// history, already reset by prior non-early duties or 36h rest.
	EarlyStartStreak int

	// UnforeseenUsedYear is how many unforeseen extensions the crew already
	// used in T's calendar year.
	UnforeseenUsedYear int

	// CompensatoryOwedMin is the outstanding compensatory-rest deficit (>0
	// means an earlier reduced rest has not been made up).
	CompensatoryOwedMin int
	// CompensatoryDueBy is the deadline by which the deficit must be made up.
	CompensatoryDueBy time.Time
}

// ProposedDuty is the trip under evaluation, normalized from a plan or a
// closed duty period.
type ProposedDuty struct {
	ReportTime  time.Time
	ReleaseTime time.Time
	SegmentCount int
	IsAugmented  bool
	SplitBreakMin int
	// AircraftFacility is the rest-facility class of the duty's aircraft type.
	// Drives R2 augmented extension. NONE disables the extension.
	AircraftFacility domain.RestFacilityClass
	// ApplyUnforeseen, if true, requests a +120min unforeseen extension (R10).
	// The evaluator validates the yearly quota and the post-extension augmented
	// rest obligation is recorded as a debt for downstream checks.
	ApplyUnforeseen bool
}

// FDP returns the proposed flight duty period duration.
func (p ProposedDuty) FDP() time.Duration {
	return p.ReleaseTime.Sub(p.ReportTime)
}

// Eval runs the full rule set and returns the result + metrics.
func Eval(in EvalInput) domain.LegalityResult {
	var v []domain.Violation
	m := domain.EvaluationMetrics{}

	// R1 + R2 + R3: FDP limit.
	m.FDPMin = int(in.Proposed.FDP().Minutes())
	m.FDPLimitMin = fdpLimitMin(in)
	if m.FDPMin > m.FDPLimitMin {
		v = append(v, domain.Violation{
			Rule:    domain.RuleFDP,
			Message: fmt.Sprintf("FDP %d min exceeds limit %d min", m.FDPMin, m.FDPLimitMin),
			Actual:  fmt.Sprintf("%d min", m.FDPMin),
			Limit:   fmt.Sprintf("%d min", m.FDPLimitMin),
		})
	}

	// R4: 28-day cumulative flight time.
	m.Used28dMin = sumBlock(in.LandedIn28d)
	m.Limit28dMin = domain.Limit28dFlightHours * 60
	if m.Used28dMin > m.Limit28dMin {
		v = append(v, domain.Violation{
			Rule:    domain.RuleCum28d,
			Message: fmt.Sprintf("28-day flight time %d min exceeds limit %d min", m.Used28dMin, m.Limit28dMin),
			Actual:  fmt.Sprintf("%d min", m.Used28dMin),
			Limit:   fmt.Sprintf("%d min", m.Limit28dMin),
		})
	}

	// R5: 168-hour cumulative FDP.
	m.Used168hMin = sumDutyFDP(in.ClosedIn168h)
	m.Limit168hMin = domain.Limit168hFDPHours * 60
	if m.Used168hMin > m.Limit168hMin {
		v = append(v, domain.Violation{
			Rule:    domain.RuleCum168h,
			Message: fmt.Sprintf("168-hour FDP %d min exceeds limit %d min", m.Used168hMin, m.Limit168hMin),
			Actual:  fmt.Sprintf("%d min", m.Used168hMin),
			Limit:   fmt.Sprintf("%d min", m.Limit168hMin),
		})
	}

	// R6: 365-day cumulative flight time.
	m.Used365dMin = sumBlock(in.LandedIn365d)
	m.Limit365dMin = domain.Limit365dFlightHours * 60
	if m.Used365dMin > m.Limit365dMin {
		v = append(v, domain.Violation{
			Rule:    domain.RuleCum365d,
			Message: fmt.Sprintf("365-day flight time %d min exceeds limit %d min", m.Used365dMin, m.Limit365dMin),
			Actual:  fmt.Sprintf("%d min", m.Used365dMin),
			Limit:   fmt.Sprintf("%d min", m.Limit365dMin),
		})
	}

	// R7: minimum rest since last duty.
	m.MinRestMin = domain.MinRestHours * 60
	if !in.PrevRelease.IsZero() {
		m.RestSinceLastMin = int(in.Proposed.ReportTime.Sub(in.PrevRelease).Minutes())
		if m.RestSinceLastMin < 0 {
			m.RestSinceLastMin = 0
		}
		if m.RestSinceLastMin < m.MinRestMin {
			// Reduced rest (9h) is allowed only if the deficit is being made up
			// within 72h and there is no outstanding compensatory debt.
			reducedFloor := domain.MinRestReducedHours * 60
			if m.RestSinceLastMin < reducedFloor {
				v = append(v, domain.Violation{
					Rule:    domain.RuleMinRest,
					Message: fmt.Sprintf("rest %d min below reduced floor %d min", m.RestSinceLastMin, reducedFloor),
					Actual:  fmt.Sprintf("%d min", m.RestSinceLastMin),
					Limit:   fmt.Sprintf("%d min", reducedFloor),
				})
			} else if in.CompensatoryOwedMin > 0 {
				v = append(v, domain.Violation{
					Rule:    domain.RuleMinRest,
					Message: fmt.Sprintf("rest reduced to %d min while %d min compensatory debt outstanding", m.RestSinceLastMin, in.CompensatoryOwedMin),
					Actual:  fmt.Sprintf("%d min", m.RestSinceLastMin),
					Limit:   fmt.Sprintf("%d min", m.MinRestMin),
				})
			}
		}
	}

	// R8: weekly rest coverage. A 168h window ending at T must contain a rest
	// of >= WeeklyRestHours (25h at home base). This requirement only applies
	// when the crew has actually flown in the window (a closed duty period in
	// ClosedIn168h); a crew proposing its first duty with no closed history has
	// no weekly-rest obligation yet.
	m.EarlyStartStreak = in.EarlyStartStreak
	if len(in.ClosedIn168h) > 0 && !hasWeeklyRest(in.RestsIn168h, in.T) {
		v = append(v, domain.Violation{
			Rule:    domain.RuleWeeklyRest,
			Message: "no weekly rest of >=30h in the trailing 168h window despite duty in window",
			Actual:  "missing",
			Limit:   fmt.Sprintf("%d h", domain.WeeklyRestHours),
		})
	}

	// R9: consecutive early starts.
	isEarly := isEarlyStart(in.Proposed.ReportTime, in.Crew.HomeTZ)
	streak := in.EarlyStartStreak
	if isEarly {
		streak++
	}
	m.EarlyStartStreak = streak
	m.EarlyStartLimit = domain.EarlyStartStreakMax
	if isEarly && streak > domain.EarlyStartStreakMax {
		v = append(v, domain.Violation{
			Rule:    domain.RuleEarlyStart,
			Message: fmt.Sprintf("early-start streak %d exceeds limit %d without 36h reset", streak, domain.EarlyStartStreakMax),
			Actual:  fmt.Sprintf("%d", streak),
			Limit:   fmt.Sprintf("%d", domain.EarlyStartStreakMax),
		})
	}

	// R10: unforeseen extension quota + post-extension augmented-rest debt.
	m.UnforeseenUsedYear = in.UnforeseenUsedYear
	m.UnforeseenLimitYear = domain.UnforeseenYearlyLimit
	if in.Proposed.ApplyUnforeseen {
		used := in.UnforeseenUsedYear
		if used >= domain.UnforeseenYearlyLimit {
			v = append(v, domain.Violation{
				Rule:    domain.RuleUnforeseenExtend,
				Message: fmt.Sprintf("unforeseen extension #%d exceeds yearly limit %d", used+1, domain.UnforeseenYearlyLimit),
				Actual:  fmt.Sprintf("%d", used+1),
				Limit:   fmt.Sprintf("%d", domain.UnforeseenYearlyLimit),
			})
		}
	}

	verdict := domain.VerdictLegal
	if len(v) > 0 {
		verdict = domain.VerdictIllegal
	}
	return domain.LegalityResult{Verdict: verdict, Violations: v, Metrics: m}
}

// fdpLimitMin computes the FDP limit in minutes for the proposed duty:
// R1 base table (report local hour × segment count) + R2 augmented extension
// + R3 split-duty credit.
func fdpLimitMin(in EvalInput) int {
	local := localHour(in.Proposed.ReportTime, in.Crew.HomeTZ)
	baseHours := domain.FDPLimitHours(local, in.Proposed.SegmentCount)
	limitMin := baseHours * 60

	// R2 augmented extension: only if the duty is augmented AND the aircraft
	// has a non-NONE rest facility.
	if in.Proposed.IsAugmented && in.Proposed.AircraftFacility != domain.FacilityNone {
		limitMin += in.Proposed.AircraftFacility.ExtensionHours() * 60
	}

	// R3 split-duty credit: a break >= 3h extends the limit by min(break, 4h).
	limitMin += domain.SplitCreditMin(in.Proposed.SplitBreakMin)
	return limitMin
}

// sumBlock sums the block_time_min of the given segments.
func sumBlock(segs []domain.FlightSegment) int {
	var n int
	for i := range segs {
		n += segs[i].BlockTimeMin
	}
	return n
}

// sumDutyFDP sums the FDP (release - report) of the given closed duty periods.
func sumDutyFDP(duties []domain.DutyPeriod) int {
	var n int
	for i := range duties {
		n += int(duties[i].FDP().Minutes())
	}
	return n
}

// localHour returns the report_time's local hour at the crew's home timezone.
// Returns the UTC hour if the timezone cannot be loaded (defensive; the crew
// store validates tz on insert, so this path is unreachable in practice).
func localHour(t time.Time, tzName string) int {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return t.UTC().Hour()
	}
	return t.In(loc).Hour()
}

// isEarlyStart reports whether the report time is before 07:00 local.
func isEarlyStart(report time.Time, tzName string) bool {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return report.UTC().Hour() < domain.EarlyStartLocalHour
	}
	return report.In(loc).Hour() < domain.EarlyStartLocalHour
}

// hasWeeklyRest reports whether the trailing 168h window ending at T contains
// a completed rest of at least WeeklyRestHours (30h), or 25h if the rest is
// at the crew's home base. A rest "covers" the weekly requirement if its span
// intersects the window and its duration is long enough.
func hasWeeklyRest(rests []domain.RestPeriod, t time.Time) bool {
	windowStart := t.Add(-domain.Window168h)
	for i := range rests {
		r := rests[i]
		// The rest must end within or at T and start at or after windowStart,
		// OR overlap the window with enough span. We use the conservative
		// "ended in window with sufficient duration" rule.
		if r.End.After(windowStart) && r.End.Before(t.Add(time.Second)) || r.End.Equal(t) {
			durHours := int(r.Duration().Hours())
			needed := domain.WeeklyRestHours
			if r.CoversWeekly {
				needed = domain.WeeklyRestHomeBaseHours
			}
			if durHours >= needed {
				return true
			}
		}
	}
	return false
}
