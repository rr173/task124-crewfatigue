package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

func TestBug10_TripCannotHaveTwoDutyPeriods(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	crewID := mustSeed(t, svc, st)
	trip, err := svc.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	dep := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	seg, err := svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738",
		ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDutyForTrip(ctx, trip.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDutyForTrip(ctx, trip.ID, false, 0); err == nil {
		t.Fatal("schedule service created a second duty for one trip")
	} else if !errors.Is(err, domain.ErrInvariantViolation) {
		t.Fatalf("duplicate duty returned unexpected error: %v", err)
	}
	raw := &domain.DutyPeriod{
		CrewID: crewID, TripID: trip.ID,
		ReportTime: dep.Add(-domain.ReportLeadMin * time.Minute),
		ReleaseTime: dep.Add(time.Hour + domain.ReleaseLagMin*time.Minute),
		Status: domain.DutyOpen,
	}
	if _, err := store.CreateDuty(ctx, st.DB(), raw, []int64{seg.ID}); err == nil {
		t.Fatal("store accepted a duplicate duty row for one trip")
	} else if !errors.Is(err, domain.ErrInvariantViolation) {
		t.Fatalf("duplicate store duty returned unexpected error: %v", err)
	}
}
