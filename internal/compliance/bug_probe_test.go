package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug09_FutureDutiesDoNotAffectEarlierRestEvaluation(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "future-history", "UTC")
	closeDuty := func(report time.Time) *domain.DutyPeriod {
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			t.Fatal(err)
		}
		dep := report.Add(domain.ReportLeadMin * time.Minute)
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
			TripID: trip.ID, AircraftType: "B738",
			ScheduledDep: dep, ScheduledArr: dep.Add(2 * time.Hour), BlockTimeMin: 120,
		}); err != nil {
			t.Fatal(err)
		}
		duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sch.CloseDuty(ctx, duty.ID, 0); err != nil {
			t.Fatal(err)
		}
		return duty
	}
	first := closeDuty(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	proposedReport := first.ReleaseTime.Add(9 * time.Hour)
	futureFirst := closeDuty(time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC))
	_ = closeDuty(futureFirst.ReleaseTime.Add(9 * time.Hour))
	eval, err := svc.EvaluateTrip(ctx, domain.EvaluateTripRequest{
		CrewID: crewID, AircraftType: "B738",
		Segments: []domain.SegmentInput{{
			DepAirport: "A", ArrAirport: "B",
			ScheduledDep: proposedReport.Add(domain.ReportLeadMin * time.Minute),
			ScheduledArr: proposedReport.Add(domain.ReportLeadMin*time.Minute + time.Hour),
		}},
	}, proposedReport.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range eval.Violations {
		if v.Rule == domain.RuleMinRest {
			t.Fatalf("future reduced-rest duties contaminated the earlier evaluation: %+v", v)
		}
	}
}

// TestBug09_RestDebtIgnoresFutureReducedRestDuties locks the as-of bound on
// the RestDebt path (computeCompensatoryDebt is shared by EvaluateTrip and
// RestDebt). A reduced-rest duty pair occurring after asOf must not contribute
// its deficit to the debt reported at the earlier instant; only the
// already-occurred reduced rest is owed.
func TestBug09_RestDebtIgnoresFutureReducedRestDuties(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "future-debt", "UTC")
	closeDuty := func(report time.Time) *domain.DutyPeriod {
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			t.Fatal(err)
		}
		dep := report.Add(domain.ReportLeadMin * time.Minute)
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
			TripID: trip.ID, AircraftType: "B738",
			ScheduledDep: dep, ScheduledArr: dep.Add(2 * time.Hour), BlockTimeMin: 120,
		}); err != nil {
			t.Fatal(err)
		}
		duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sch.CloseDuty(ctx, duty.ID, 0); err != nil {
			t.Fatal(err)
		}
		return duty
	}
	// Past pair: two duties with a 9h (reduced) rest between them. Both close
	// before asOf, so the 60-min deficit is owed at asOf.
	d0 := closeDuty(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	closeDuty(d0.ReleaseTime.Add(9 * time.Hour))
	// Future pair: another reduced-rest pair entirely after asOf. With the
	// as-of bound this pair is invisible at the earlier instant; without it,
	// its deficit would double the reported debt.
	d2 := closeDuty(time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))
	closeDuty(d2.ReleaseTime.Add(9 * time.Hour))

	asOf := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	debt, err := svc.RestDebt(ctx, crewID, asOf)
	if err != nil {
		t.Fatalf("debt: %v", err)
	}
	// Only the past reduced rest (60 min) is owed; the future pair adds nothing.
	const want = domain.MinRestHours*60 - domain.MinRestReducedHours*60
	if debt.CompensatoryOwedMin != want {
		t.Errorf("CompensatoryOwedMin=%d want %d (future reduced-rest duties leaked into as-of replay)", debt.CompensatoryOwedMin, want)
	}
	if debt.CompensatoryDueBy == nil {
		t.Fatal("CompensatoryDueBy nil; want the past deficit's deadline")
	}
	// Past reduced rest: duty 1 reports at d0.ReleaseTime + 9h; deadline is
	// that report + the compensatory make-up window.
	wantDue := d0.ReleaseTime.Add(9 * time.Hour).Add(domain.CompensatoryWindowHours * time.Hour)
	if !debt.CompensatoryDueBy.Equal(wantDue) {
		t.Errorf("CompensatoryDueBy=%s want %s", debt.CompensatoryDueBy.UTC(), wantDue.UTC())
	}
}
