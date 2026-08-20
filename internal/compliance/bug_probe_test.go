package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func TestBug03_ShortAugmentedRestDoesNotClearDebt(t *testing.T) {
	svc, st, sch := newSvc(t)
	ctx := context.Background()
	crewID := seedCrew(t, st, "short-augmented", "UTC")
	dep := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil { t.Fatal(err) }
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{TripID: trip.ID, AircraftType: "B738", ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60}); err != nil { t.Fatal(err) }
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil { t.Fatal(err) }
	if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { t.Fatal(err) }
	start := duty.ReleaseTime.Add(24*time.Hour)
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{CrewID: crewID, Start: start, End: start.Add(13*time.Hour), RestType: domain.RestAugmented}); err != nil { t.Fatal(err) }
	debt, err := svc.RestDebt(ctx, crewID, start.Add(14*time.Hour))
	if err != nil { t.Fatal(err) }
	if !debt.OwesAugmentedRest { t.Fatal("short augmented rest incorrectly cleared the R10 debt") }
}
