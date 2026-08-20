package compliance

import (
	"context"
	"errors"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

func TestBug06_PersistedDutyCannotMixAircraftFacilities(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "mixed-aircraft", "UTC")
	dep := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	first := &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738",
		ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
	}
	if _, err := sch.AddSegment(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B789",
		ScheduledDep: dep.Add(2 * time.Hour), ScheduledArr: dep.Add(3 * time.Hour), BlockTimeMin: 60,
	}
	if _, err := sch.AddSegment(ctx, second); err == nil {
		t.Fatal("schedule accepted a second aircraft facility for the same trip")
	} else if !errors.Is(err, domain.ErrInvariantViolation) {
		t.Fatalf("mixed aircraft returned unexpected error: %v", err)
	}
	if _, err := store.CreateSegment(ctx, st.DB(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EvaluatePersistedTrip(ctx, crewID, trip.ID, true, 0, false, dep.Add(12*time.Hour)); err == nil {
		t.Fatal("persisted mixed-aircraft trip was evaluated using only the first facility")
	} else if !errors.Is(err, domain.ErrInvariantViolation) {
		t.Fatalf("legacy mixed aircraft returned unexpected error: %v", err)
	}
}
