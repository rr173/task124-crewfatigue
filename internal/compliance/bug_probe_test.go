package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug08_FutureAugmentedRestDoesNotClearDebtEarly(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "augmented-asof", "UTC")
	dep := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738",
		ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
	}); err != nil {
		t.Fatal(err)
	}
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil {
		t.Fatal(err)
	}
	restStart := dep.Add(24 * time.Hour)
	restEnd := restStart.Add(domain.AugmentedRestHours * time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: restStart, End: restEnd,
		RestType: domain.RestAugmented, DurationMin: domain.AugmentedRestHours * 60,
	}); err != nil {
		t.Fatal(err)
	}
	debt, err := svc.RestDebt(ctx, crewID, restStart.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !debt.OwesAugmentedRest {
		t.Fatal("augmented-rest debt was cleared before the rest completed")
	}
}
