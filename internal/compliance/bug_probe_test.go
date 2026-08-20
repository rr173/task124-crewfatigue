package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug04_LateCompensatoryRestDoesNotClearDebt(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "late-comp", "UTC")
	closeDuty := func(dep time.Time) *domain.DutyPeriod {
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			t.Fatal(err)
		}
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
	second := closeDuty(first.ReleaseTime.Add(10 * time.Hour))
	deadline := second.ReportTime.Add(domain.CompensatoryWindowHours * time.Hour)
	restStart := deadline.Add(-2 * time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: restStart, End: deadline.Add(time.Hour),
		RestType: domain.RestCompensatory, DurationMin: 60,
	}); err != nil {
		t.Fatal(err)
	}
	debt, err := svc.RestDebt(ctx, crewID, deadline.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if debt.CompensatoryOwedMin != 60 {
		t.Fatalf("late compensatory rest cleared debt: got %d, want 60", debt.CompensatoryOwedMin)
	}
}

// TestBug04_OnTimeCompensatoryRestClearsDebt is the positive complement: a
// compensatory rest completed within the 72h make-up window clears the
// reduced-rest deficit. Guards against the fix over-rejecting timely rests.
func TestBug04_OnTimeCompensatoryRestClearsDebt(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "ontime-comp", "UTC")
	closeDuty := func(dep time.Time) *domain.DutyPeriod {
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			t.Fatal(err)
		}
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
	second := closeDuty(first.ReleaseTime.Add(10 * time.Hour))
	deadline := second.ReportTime.Add(domain.CompensatoryWindowHours * time.Hour)
	// Compensatory rest completed 1h before the deadline — inside the 72h
	// make-up window — clears the 60min reduced-rest deficit.
	restStart := deadline.Add(-3 * time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: restStart, End: deadline.Add(-1 * time.Hour),
		RestType: domain.RestCompensatory, DurationMin: 60,
	}); err != nil {
		t.Fatal(err)
	}
	debt, err := svc.RestDebt(ctx, crewID, deadline.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if debt.CompensatoryOwedMin != 0 {
		t.Fatalf("on-time compensatory rest did not clear debt: got %d, want 0", debt.CompensatoryOwedMin)
	}
}
