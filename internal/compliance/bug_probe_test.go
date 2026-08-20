package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

func TestBug02_FutureExtensionsDoNotConsumePastQuota(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "quota-asof", "UTC")
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		t.Fatal(err)
	}
	dep := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
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
	if _, err := sch.CloseDuty(ctx, duty.ID, 0); err != nil {
		t.Fatal(err)
	}
	future := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if _, err := store.AppendEvent(ctx, st.DB(), &domain.ComplianceEvent{
			CrewID: crewID, Ts: future.Add(time.Duration(i) * time.Hour),
			Kind: domain.EventUnforeseenExtended,
			PayloadJSON: "{\"duty_id\":99,\"added_min\":120}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	eval, err := svc.EvaluatePersistedTrip(ctx, crewID, trip.ID, false, 0, true, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range eval.Violations {
		if v.Rule == domain.RuleUnforeseenExtend {
			t.Fatalf("future extension was counted at the earlier as-of time: %+v", v)
		}
	}
}
