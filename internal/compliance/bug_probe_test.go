package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug05_UnforeseenExtensionExpandsPersistedDutyFDP(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "unforeseen-fdp", "UTC")
	base := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil { t.Fatal(err) }
	deps := []time.Duration{0, 100*time.Minute, 200*time.Minute, 300*time.Minute, 400*time.Minute, 500*time.Minute, 595*time.Minute}
	for _, off := range deps {
		dep := base.Add(off)
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", ScheduledDep: dep, ScheduledArr: dep.Add(30*time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
	}
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil { t.Fatal(err) }
	if duty.FDP().Minutes() <= 660 || duty.FDP().Minutes() > 780 { t.Fatalf("test FDP=%d", int(duty.FDP().Minutes())) }
	if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { t.Fatal(err) }
	restStart := duty.ReleaseTime.Add(time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: restStart, End: restStart.Add(30*time.Hour), RestType: domain.RestNormal, CoversWeekly: true}); err != nil { t.Fatal(err) }
	eval, err := svc.EvaluatePersistedTrip(ctx, crewID, trip.ID, false, 0, true, base.Add(72*time.Hour))
	if err != nil { t.Fatal(err) }
	for _, v := range eval.Violations {
		if v.Rule == domain.RuleFDP { t.Fatalf("unforeseen extension did not expand FDP limit: violations=%+v metrics=%+v", eval.Violations, eval.Metrics) }
	}
	if eval.Metrics.FDPLimitMin != 600+120 {
		t.Fatalf("extended fdp_limit=%d want 720", eval.Metrics.FDPLimitMin)
	}
	if eval.Verdict != domain.VerdictLegal {
		t.Fatalf("within-quota extension should be LEGAL, got %s %+v", eval.Verdict, eval.Violations)
	}
}

// TestBug05_UnforeseenExtensionStillRespectsYearlyLimit verifies that raising
// the FDP limit did not disable the annual count limit (R10): after two
// unforeseen extensions in the year (the limit), a third requested extension is
// flagged ILLEGAL with an R10 violation even though the FDP itself fits.
func TestBug05_UnforeseenExtensionStillRespectsYearlyLimit(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "unfor-quota", "UTC")
	// Two prior unforeseen extensions exhaust the yearly quota (limit 2).
	for i := 0; i < 2; i++ {
		dep := time.Date(2026, 8, 1+i, 8, 0, 0, 0, time.UTC)
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil { t.Fatal(err) }
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", ScheduledDep: dep, ScheduledArr: dep.Add(30 * time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
		duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
		if err != nil { t.Fatal(err) }
		if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { t.Fatal(err) }
	}
	// Weekly rest so R8 does not fire for the proposed third duty.
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 2, 9, 45, 0, 0, time.UTC), End: time.Date(2026, 8, 3, 15, 45, 0, 0, time.UTC), RestType: domain.RestNormal, DurationMin: 30 * 60, CoversWeekly: true}); err != nil { t.Fatal(err) }
	// A small third trip whose FDP fits the (extended) limit, so only R10 fires.
	trip3, err := sch.CreateTrip(ctx, crewID)
	if err != nil { t.Fatal(err) }
	d1 := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip3.ID, AircraftType: "B738", ScheduledDep: d1, ScheduledArr: d1.Add(30 * time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
	d2 := d1.Add(time.Hour)
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip3.ID, AircraftType: "B738", ScheduledDep: d2, ScheduledArr: d2.Add(30 * time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
	eval, err := svc.EvaluatePersistedTrip(ctx, crewID, trip3.ID, false, 0, true, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	if err != nil { t.Fatal(err) }
	if !hasRule(eval.Violations, domain.RuleUnforeseenExtend) {
		t.Fatalf("yearly limit not respected: expected R10 violation, got %+v", eval.Violations)
	}
	if eval.Verdict != domain.VerdictIllegal {
		t.Fatalf("over-quota extension should be ILLEGAL, got %s %+v", eval.Verdict, eval.Violations)
	}
	if hasRule(eval.Violations, domain.RuleFDP) {
		t.Fatalf("small FDP should not trip R1: %+v", eval.Violations)
	}
}

// TestBug05_UnforeseenExtensionRefreshesFDPViolation verifies that when the FDP
// still exceeds even the extended limit, the R1 violation is kept and its limit
// refreshed to the extended value (not dropped, not left stale at the base).
func TestBug05_UnforeseenExtensionRefreshesFDPViolation(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "unfor-refresh", "UTC")
	base := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil { t.Fatal(err) }
	// 7 segments at night (report 04:00Z local 04 → 夜间 7+ → 600 base).
	// Last dep at +695min → release 17:20Z → FDP 800min > extended 720.
	deps := []time.Duration{0, 100 * time.Minute, 200 * time.Minute, 300 * time.Minute, 400 * time.Minute, 500 * time.Minute, 695 * time.Minute}
	for _, off := range deps {
		dep := base.Add(off)
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", ScheduledDep: dep, ScheduledArr: dep.Add(30 * time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
	}
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil { t.Fatal(err) }
	if duty.FDP().Minutes() <= 720 { t.Fatalf("test FDP=%d want >720", int(duty.FDP().Minutes())) }
	if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { t.Fatal(err) }
	restStart := duty.ReleaseTime.Add(time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: restStart, End: restStart.Add(30 * time.Hour), RestType: domain.RestNormal, CoversWeekly: true}); err != nil { t.Fatal(err) }
	eval, err := svc.EvaluatePersistedTrip(ctx, crewID, trip.ID, false, 0, true, base.Add(72*time.Hour))
	if err != nil { t.Fatal(err) }
	var fdp *domain.Violation
	for i := range eval.Violations {
		if eval.Violations[i].Rule == domain.RuleFDP {
			fdp = &eval.Violations[i]
			break
		}
	}
	if fdp == nil {
		t.Fatalf("expected refreshed R1 violation (FDP still exceeds extended limit), got %+v", eval.Violations)
	}
	if fdp.Limit != "720 min" {
		t.Fatalf("R1 limit not refreshed to extended value: got %q want 720 min", fdp.Limit)
	}
	if eval.Metrics.FDPLimitMin != 720 {
		t.Fatalf("extended fdp_limit=%d want 720", eval.Metrics.FDPLimitMin)
	}
	if hasRule(eval.Violations, domain.RuleUnforeseenExtend) {
		t.Fatalf("within-quota extension should not trip R10: %+v", eval.Violations)
	}
	if eval.Verdict != domain.VerdictIllegal {
		t.Fatalf("FDP exceeding extended limit should be ILLEGAL, got %s", eval.Verdict)
	}
}

func hasRule(vs []domain.Violation, code domain.RuleCode) bool {
	for _, v := range vs {
		if v.Rule == code {
			return true
		}
	}
	return false
}
