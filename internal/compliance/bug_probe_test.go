package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug05_UnforeseenExtensionExpandsPersistedDutyFDP(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "unforeseen-fdp", "UTC")
	base := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil { t.Fatal(err) }
	deps := []time.Duration{0, 100*time.Minute, 200*time.Minute, 300*time.Minute, 400*time.Minute, 500*time.Minute, 595*time.Minute}
	for _, off := range deps {
		dep := base.Add(off)
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", ScheduledDep: dep, ScheduledArr: dep.Add(30*time.Minute), BlockTimeMin: 30}); err != nil { t.Fatal(err) }
	}
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil { t.Fatal(err) }
	if duty.FDP().Minutes() <= 660 || duty.FDP().Minutes() > 780 { t.Fatalf("test FDP=%d", int(duty.FDP().Minutes())) }
	if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { t.Fatal(err) }
	restStart := duty.ReleaseTime.Add(time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: restStart, End: restStart.Add(30*time.Hour), RestType: domain.RestNormal, CoversWeekly: true}); err != nil { t.Fatal(err) }
	eval, err := svc.EvaluatePersistedTrip(ctx, crewID, trip.ID, false, 0, true, base.Add(72*time.Hour))
	if err != nil { t.Fatal(err) }
	for _, v := range eval.Violations {
		if v.Rule == domain.RuleFDP { t.Fatalf("unforeseen extension did not expand FDP limit: violations=%+v metrics=%+v", eval.Violations, eval.Metrics) }
	}
}
