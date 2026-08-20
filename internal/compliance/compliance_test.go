package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/store"
)

func newSvc(t *testing.T) (*Service, *store.Store, *schedule.Service) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sch := schedule.New(st)
	return New(st, sch), st, sch
}

func seedCrew(t *testing.T, st *store.Store, name, tz string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := store.CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: name, Role: domain.RoleCaptain, HomeTZ: tz})
	if err != nil {
		t.Fatalf("crew: %v", err)
	}
	if err := store.CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone}); err != nil {
		t.Fatalf("aircraft: %v", err)
	}
	if err := store.CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B789", HasRestFacility: true, RestFacilityClass: domain.FacilityClass1}); err != nil {
		t.Fatalf("aircraft B789: %v", err)
	}
	return id
}

func TestEvaluateLegalTrip(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "L", "Asia/Shanghai")
	// weekly rest so R8 won't fire (no closed duty, but the rest is harmless).
	sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), RestType: domain.RestNormal, DurationMin: 30 * 60})
	req := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)},
		{DepAirport: "SHA", ArrAirport: "PEK", ScheduledDep: time.Date(2026, 8, 18, 6, 30, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 18, 7, 30, 0, 0, time.UTC)},
	}}
	ev, err := svc.EvaluateTrip(ctx, req, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if ev.Verdict != domain.VerdictLegal {
		t.Errorf("want LEGAL got %s %+v", ev.Verdict, ev.Violations)
	}
	if ev.Metrics.FDPMin != 225 || ev.Metrics.FDPLimitMin != 840 {
		t.Errorf("FDP=%d limit=%d want 225/840", ev.Metrics.FDPMin, ev.Metrics.FDPLimitMin)
	}
}

func TestCumulativeLive(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "C", "Asia/Shanghai")
	sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), RestType: domain.RestNormal, DurationMin: 30 * 60})
	trip, _ := sch.CreateTrip(ctx, crewID)
	sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", DepAirport: "A", ArrAirport: "B", ScheduledDep: time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 17, 7, 0, 0, 0, time.UTC), BlockTimeMin: 60})
	duty, _ := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	sch.CloseDuty(ctx, duty.ID, 0)
	snap, err := svc.Cumulative(ctx, crewID, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("cumulative: %v", err)
	}
	if snap.Used28dMin != 60 {
		t.Errorf("used_28d=%d want 60", snap.Used28dMin)
	}
}

func TestReplayFreshAndConverge(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "R", "Asia/Shanghai")
	// Fresh: 0 overrides.
	overrides, _, err := svc.ReplayForCrew(ctx, crewID, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("replay fresh: %v", err)
	}
	if overrides != 0 {
		t.Errorf("fresh overrides=%d want 0", overrides)
	}
	// Close a duty with a landed segment, then replay → snapshots get written
	// (every window 0→value differs), so overrides == 3.
	sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), RestType: domain.RestNormal, DurationMin: 30 * 60})
	trip, _ := sch.CreateTrip(ctx, crewID)
	sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", DepAirport: "A", ArrAirport: "B", ScheduledDep: time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC), ScheduledArr: time.Date(2026, 8, 17, 7, 0, 0, 0, time.UTC), BlockTimeMin: 60})
	duty, _ := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	sch.CloseDuty(ctx, duty.ID, 0)
	overrides2, snap, err := svc.ReplayForCrew(ctx, crewID, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("replay 1: %v", err)
	}
	if overrides2 != 3 {
		t.Errorf("overrides=%d want 3", overrides2)
	}
	if snap.Used28dMin != 60 {
		t.Errorf("used_28d=%d want 60", snap.Used28dMin)
	}
	// Replay again: snapshots now match → 0.
	overrides3, _, err := svc.ReplayForCrew(ctx, crewID, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("replay 2: %v", err)
	}
	if overrides3 != 0 {
		t.Errorf("converge overrides=%d want 0", overrides3)
	}
}

func TestRestDebt(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "D", "Asia/Shanghai")
	// Register a rest via the schedule service (exercises the REST_COMPLETED path).
	sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), RestType: domain.RestNormal, DurationMin: 30 * 60})
	// No compensatory debt from a normal rest.
	debt, err := svc.RestDebt(ctx, crewID, time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("debt: %v", err)
	}
	if debt.CompensatoryOwedMin != 0 || debt.OwesAugmentedRest {
		t.Errorf("fresh debt mismatch: %+v", debt)
	}
}
