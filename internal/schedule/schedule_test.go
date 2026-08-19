package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st), st
}

func mustSeed(t *testing.T, svc *Service, st *store.Store) (crewID int64) {
	t.Helper()
	ctx := context.Background()
	id, err := store.CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "Z", Role: domain.RoleCaptain, HomeTZ: "Asia/Shanghai"})
	if err != nil {
		t.Fatalf("crew: %v", err)
	}
	if err := store.CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone}); err != nil {
		t.Fatalf("aircraft: %v", err)
	}
	return id
}

func TestTripSegmentFlow(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	crewID := mustSeed(t, svc, st)
	trip, err := svc.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatalf("trip: %v", err)
	}
	seg, err := svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
		ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("segment: %v", err)
	}
	list, err := svc.ListSegments(ctx, trip.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != seg.ID {
		t.Errorf("list mismatch: %+v", list)
	}
	// Unknown aircraft rejected.
	if _, err := svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "ZZZ", ScheduledDep: time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC),
	}); !errors.Is(err, domain.ErrAircraftNotFound) {
		t.Errorf("unknown aircraft: want ErrAircraftNotFound got %v", err)
	}
}

func TestDutyCreateClose(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	crewID := mustSeed(t, svc, st)
	trip, _ := svc.CreateTrip(ctx, crewID)
	svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
		ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC),
	})
	svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738", DepAirport: "SHA", ArrAirport: "PEK",
		ScheduledDep: time.Date(2026, 8, 18, 6, 30, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 7, 30, 0, 0, time.UTC),
	})
	duty, err := svc.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil {
		t.Fatalf("duty: %v", err)
	}
	// report 04:00, release 07:45 → FDP 225.
	if got := duty.FDP(); got != 225*time.Minute {
		t.Errorf("FDP=%v want 225m", got)
	}
	if len(duty.Segments) != 2 {
		t.Errorf("segments=%d want 2", len(duty.Segments))
	}
	closed, err := svc.CloseDuty(ctx, duty.ID, 0)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Status != domain.DutyClosed {
		t.Errorf("status=%v want CLOSED", closed.Status)
	}
	// Closing again fails.
	if _, err := svc.CloseDuty(ctx, duty.ID, 0); !errors.Is(err, domain.ErrDutyNotOpen) {
		t.Errorf("second close: want ErrDutyNotOpen got %v", err)
	}
	// The trip is now unplanned; adding a segment fails.
	if _, err := svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738", ScheduledDep: time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC),
	}); !errors.Is(err, domain.ErrDutyClosed) {
		t.Errorf("add after close: want ErrDutyClosed got %v", err)
	}
}

func TestDutyUnforeseenClose(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	crewID := mustSeed(t, svc, st)
	trip, _ := svc.CreateTrip(ctx, crewID)
	svc.AddSegment(ctx, &domain.FlightSegment{
		TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
		ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC),
	})
	duty, _ := svc.CreateDutyForTrip(ctx, trip.ID, false, 0)
	closed, err := svc.CloseDuty(ctx, duty.ID, 200) // capped at 120
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.UnforeseenExtensionMin != 120 {
		t.Errorf("extension=%d want 120 (capped)", closed.UnforeseenExtensionMin)
	}
	if !closed.OweAugmentedRest {
		t.Error("extension should set owe_augmented_rest")
	}
}

func TestRestCreate(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	crewID := mustSeed(t, svc, st)
	r, err := svc.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC),
		End: time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC), RestType: domain.RestNormal,
	})
	if err != nil {
		t.Fatalf("rest: %v", err)
	}
	if r.DurationMin != 600 {
		t.Errorf("duration=%d want 600", r.DurationMin)
	}
	// Invalid rest type rejected.
	if _, err := svc.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC),
		End: time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC), RestType: "BOGUS",
	}); !errors.Is(err, domain.ErrRestTypeInvalid) {
		t.Errorf("bad rest type: want ErrRestTypeInvalid got %v", err)
	}
	// end before start rejected.
	if _, err := svc.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC),
		End: time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC), RestType: domain.RestNormal,
	}); !errors.Is(err, domain.ErrInvariantViolation) {
		t.Errorf("end-before-start: want ErrInvariantViolation got %v", err)
	}
}
