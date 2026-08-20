package compliance

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/schedule"
)

// TestBug08_AugmentedRestClearsDebtOnlyWhenCompletedAndLongEnough covers the
// cases the fix must NOT over-correct, complementing
// TestBug08_FutureAugmentedRestDoesNotClearDebtEarly (which pins the "not yet
// ended" time-boundary case):
//   - a properly completed (end <= asOf) augmented rest of >= 14h clears the
//     debt, and
//   - a too-short augmented rest (below 14h), even once completed, does NOT
//     clear it.
func TestBug08_AugmentedRestClearsDebtOnlyWhenCompletedAndLongEnough(t *testing.T) {
	ctx := context.Background()

	// mk seeds a crew and closes a single-segment duty with a +2h unforeseen
	// extension, so an augmented-rest debt is owed. It returns the compliance
	// service, the schedule service, the crew id and the segment departure.
	mk := func(name string) (svc *Service, sch *schedule.Service, crewID int64, dep time.Time) {
		svc, st, sch := newSvc(t)
		crewID = seedCrew(t, st, name, "UTC")
		dep = time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
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
		if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { // +2h unforeseen -> owes augmented rest
			t.Fatal(err)
		}
		return svc, sch, crewID, dep
	}

	// Case A: a qualifying augmented rest (14h) completed before asOf clears the debt.
	svcA, schA, crewA, depA := mk("aug-cleared")
	restStartA := depA.Add(24 * time.Hour)
	restEndA := restStartA.Add(domain.AugmentedRestHours * time.Hour)
	if _, err := schA.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewA, Start: restStartA, End: restEndA,
		RestType: domain.RestAugmented, DurationMin: domain.AugmentedRestHours * 60,
	}); err != nil {
		t.Fatal(err)
	}
	debtA, err := svcA.RestDebt(ctx, crewA, restEndA.Add(time.Hour)) // asOf after the rest ended
	if err != nil {
		t.Fatal(err)
	}
	if debtA.OwesAugmentedRest {
		t.Fatalf("case A: a completed 14h augmented rest should clear the debt, got owes=true")
	}

	// Case B: a too-short augmented rest (13h) completed before asOf does NOT clear.
	svcB, schB, crewB, depB := mk("aug-short")
	restStartB := depB.Add(24 * time.Hour)
	restEndB := restStartB.Add((domain.AugmentedRestHours - 1) * time.Hour) // 13h < 14h
	if _, err := schB.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewB, Start: restStartB, End: restEndB,
		RestType: domain.RestAugmented, DurationMin: (domain.AugmentedRestHours - 1) * 60,
	}); err != nil {
		t.Fatal(err)
	}
	debtB, err := svcB.RestDebt(ctx, crewB, restEndB.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !debtB.OwesAugmentedRest {
		t.Fatalf("case B: a 13h augmented rest (below the 14h minimum) must not clear the debt")
	}
}
