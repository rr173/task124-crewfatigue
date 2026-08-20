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
