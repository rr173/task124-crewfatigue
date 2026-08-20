package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

func TestBug07_PersistedEvaluationCannotCrossCrewBoundary(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	ownerID := seedCrew(t, st, "owner", "UTC")
	otherID, err := store.CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "other", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	trip, err := sch.CreateTrip(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	dep := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738",
		ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EvaluatePersistedTrip(ctx, otherID, trip.ID, false, 0, false, dep.Add(12*time.Hour)); err == nil {
		t.Fatal("crew member evaluated another crew's persisted trip")
	}
}
