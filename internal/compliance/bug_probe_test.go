package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug01_CumulativeUsesActualDepartureAfterReplay(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "actual-departure", "UTC")
	scheduledDep := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	actualDep := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738",
		ScheduledDep: scheduledDep, ScheduledArr: scheduledDep.Add(2 * time.Hour),
		ActualDep: actualDep, ActualArr: actualDep.Add(2 * time.Hour), BlockTimeMin: 120,
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
	snap, err := svc.Cumulative(ctx, crewID, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Used28dMin != 0 {
		t.Fatalf("28-day usage=%d, want 0 because actual departure is outside the window", snap.Used28dMin)
	}
}
