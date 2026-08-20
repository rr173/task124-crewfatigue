// Package selfcheck runs the --smoke-test: it opens an in-memory SQLite,
// registers crew + aircraft, builds planned trips and closed-duty histories,
// evaluates the R1-R10 rules against hand-computed expectations, exercises the
// recovery path (ReplayForCrew + snapshot-override) and serves the frontend +
// business API via an httptest server. It exits 0 on success, 1 on any
// failure. No external service and no real-time sleeps; all times are pinned
// absolute instants so the cumulative windows are deterministic.
package selfcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"task124-crewfatigue/internal/compliance"
	"task124-crewfatigue/internal/crew"
	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/httpapi"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/store"
	"task124-crewfatigue/internal/webfs"
)

// T0 is the pinned evaluation instant for all selfcheck scenarios. It is a
// fixed UTC instant so the 28-day/168-hour/365-day windows are deterministic.
var T0 = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

// Run executes the full smoke scenario against an in-memory database and an
// httptest server. Returns nil on success.
func Run() error {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	cs := crew.New(st)
	sch := schedule.New(st)
	cps := compliance.New(st, sch)
	svc := httpapi.Services{Store: st, Crew: cs, Schedule: sch, Compliance: cps}

	mux := httpapi.NewMux(svc, webfs.HTTPFS())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &client{base: srv.URL}

	// Seed base data shared across assertions.
	if err := seedBase(ctx, cs); err != nil {
		return err
	}

	// --- planned-trip FDP rules (no closed-duty history needed) ---
	if err := fdpLegalAssertion(ctx, cps); err != nil {
		return err
	}
	if err := fdpExceedAssertion(ctx, cps); err != nil {
		return err
	}
	if err := augmentedExtensionAssertion(ctx, cps); err != nil {
		return err
	}
	if err := splitDutyAssertion(ctx, cps); err != nil {
		return err
	}

	// --- rules needing closed-duty history, each on its own crew ---
	if err := cumulativeAssertion(ctx, cs, sch, cps); err != nil {
		return err
	}
	if err := minRestAssertion(ctx, cs, sch, cps); err != nil {
		return err
	}
	if err := earlyStartAssertion(ctx, cs, sch, cps); err != nil {
		return err
	}
	if err := unforeseenQuotaAssertion(ctx, cs, sch, cps); err != nil {
		return err
	}

	// --- recovery / replay ---
	if err := recoveryReplayAssertion(ctx, sch, cps); err != nil {
		return err
	}

	// --- frontend + API via httptest ---
	if err := frontendAssertions(c); err != nil {
		return err
	}
	fmt.Println("smoke OK")
	return nil
}

// RunAndExit runs Run and exits with the appropriate code.
func RunAndExit() {
	if err := Run(); err != nil {
		fmt.Fprintf(os.Stderr, "smoke FAIL: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// seedBase registers the aircraft types and a captain used by the planned-trip
// FDP assertions (crew 1).
func seedBase(ctx context.Context, cs *crew.Service) error {
	if err := cs.RegisterAircraft(ctx, &domain.AircraftType{Code: "B789", HasRestFacility: true, RestFacilityClass: domain.FacilityClass1}); err != nil {
		return fmt.Errorf("seed B789: %w", err)
	}
	if err := cs.RegisterAircraft(ctx, &domain.AircraftType{Code: "B738", HasRestFacility: false, RestFacilityClass: domain.FacilityNone}); err != nil {
		return fmt.Errorf("seed B738: %w", err)
	}
	if err := cs.RegisterAircraft(ctx, &domain.AircraftType{Code: "A321", HasRestFacility: true, RestFacilityClass: domain.FacilityClass2}); err != nil {
		return fmt.Errorf("seed A321: %w", err)
	}
	if _, err := cs.Register(ctx, &domain.CrewMember{Name: "Zhang Wei", Role: domain.RoleCaptain, HomeBase: "PEK", HomeTZ: "Asia/Shanghai"}); err != nil {
		return fmt.Errorf("seed crew: %w", err)
	}
	return nil
}

// registerCrew is a helper to register a crew and return its ID.
func registerCrew(ctx context.Context, cs *crew.Service, name string, role domain.CrewRole, tz string) (int64, error) {
	return cs.Register(ctx, &domain.CrewMember{Name: name, Role: role, HomeBase: "PEK", HomeTZ: tz})
}

// fdpLegalAssertion: a 2-segment昼间 trip whose FDP fits the 14h limit (R1).
// report 04:00Z (local 12:00昼间), release 07:45Z, FDP 225min < 840 (14h).
func fdpLegalAssertion(ctx context.Context, cps *compliance.Service) error {
	req := domain.EvaluateTripRequest{
		CrewID: 1, AircraftType: "B738",
		Segments: []domain.SegmentInput{
			{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-18T05:00:00Z"), ScheduledArr: t("2026-08-18T06:00:00Z")},
			{DepAirport: "SHA", ArrAirport: "PEK", ScheduledDep: t("2026-08-18T06:30:00Z"), ScheduledArr: t("2026-08-18T07:30:00Z")},
		},
	}
	ev, err := cps.EvaluateTrip(ctx, req, T0)
	if err != nil {
		return fmt.Errorf("evaluate legal: %w", err)
	}
	if ev.Verdict != domain.VerdictLegal {
		return fmt.Errorf("legal trip: verdict=%s want LEGAL (violations=%v)", ev.Verdict, ev.Violations)
	}
	if ev.Metrics.FDPLimitMin != 840 {
		return fmt.Errorf("legal trip fdp_limit=%d want 840", ev.Metrics.FDPLimitMin)
	}
	if ev.Metrics.FDPMin != 225 {
		return fmt.Errorf("legal trip fdp=%d want 225", ev.Metrics.FDPMin)
	}
	return nil
}

// fdpExceedAssertion: a 7-segment昼间 trip whose FDP (835min) exceeds the 11h
// limit (R1, 7+ segments by day → 660min).
func fdpExceedAssertion(ctx context.Context, cps *compliance.Service) error {
	segs := make([]domain.SegmentInput, 7)
	for i := 0; i < 7; i++ {
		dep := t(fmt.Sprintf("2026-08-18T%02d:00:00Z", 5+i*2)) // 05..17Z, all昼间 local
		arr := dep.Add(40 * time.Minute)
		segs[i] = domain.SegmentInput{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: dep, ScheduledArr: arr}
	}
	// last arr 17:40Z, release 17:55Z, report 04:00Z → FDP 13h55m=835min > 660.
	req := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B738", Segments: segs}
	ev, err := cps.EvaluateTrip(ctx, req, T0)
	if err != nil {
		return fmt.Errorf("evaluate exceed: %w", err)
	}
	if ev.Verdict != domain.VerdictIllegal {
		return fmt.Errorf("exceed trip: verdict=%s want ILLEGAL", ev.Verdict)
	}
	if !hasRule(ev.Violations, domain.RuleFDP) {
		return fmt.Errorf("exceed trip: missing R1 violation (got %v)", ev.Violations)
	}
	if ev.Metrics.FDPLimitMin != 660 {
		return fmt.Errorf("exceed trip fdp_limit=%d want 660", ev.Metrics.FDPLimitMin)
	}
	return nil
}

// augmentedExtensionAssertion: R2 — the exceeding trip becomes legal under an
// augmented B789 (CLASS_1 +4h → 660+240=900). A B738 (NONE) stays at 660.
func augmentedExtensionAssertion(ctx context.Context, cps *compliance.Service) error {
	segs := make([]domain.SegmentInput, 7)
	for i := 0; i < 7; i++ {
		dep := t(fmt.Sprintf("2026-08-18T%02d:00:00Z", 5+i*2))
		arr := dep.Add(40 * time.Minute)
		segs[i] = domain.SegmentInput{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: dep, ScheduledArr: arr}
	}
	req := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B789", IsAugmented: true, Segments: segs}
	ev, err := cps.EvaluateTrip(ctx, req, T0)
	if err != nil {
		return fmt.Errorf("evaluate augmented: %w", err)
	}
	if ev.Metrics.FDPLimitMin != 900 {
		return fmt.Errorf("augmented fdp_limit=%d want 900", ev.Metrics.FDPLimitMin)
	}
	if ev.Verdict != domain.VerdictLegal {
		return fmt.Errorf("augmented trip: verdict=%s want LEGAL (violations=%v)", ev.Verdict, ev.Violations)
	}
	// Non-augmented on the same wide-body: no extension (R2 requires augmented).
	req2 := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B789", IsAugmented: false, Segments: segs}
	ev2, _ := cps.EvaluateTrip(ctx, req2, T0)
	if ev2.Metrics.FDPLimitMin != 660 {
		return fmt.Errorf("B789 non-augmented fdp_limit=%d want 660 (no extension without augmented crew)", ev2.Metrics.FDPLimitMin)
	}
	// Augmented on a NONE-facility type: no extension.
	req3 := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B738", IsAugmented: true, Segments: segs}
	ev3, _ := cps.EvaluateTrip(ctx, req3, T0)
	if ev3.Metrics.FDPLimitMin != 660 {
		return fmt.Errorf("B738 augmented (no facility) fdp_limit=%d want 660", ev3.Metrics.FDPLimitMin)
	}
	return nil
}

// splitDutyAssertion: R3 — a 4h in-duty break credits +4h to the FDP limit.
func splitDutyAssertion(ctx context.Context, cps *compliance.Service) error {
	segs := []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-18T05:00:00Z"), ScheduledArr: t("2026-08-18T06:00:00Z")},
		{DepAirport: "SHA", ArrAirport: "PEK", ScheduledDep: t("2026-08-18T10:30:00Z"), ScheduledArr: t("2026-08-18T11:30:00Z")},
		{DepAirport: "PEK", ArrAirport: "CAN", ScheduledDep: t("2026-08-18T19:00:00Z"), ScheduledArr: t("2026-08-18T20:00:00Z")},
	}
	// report 04:00Z, release 20:15Z → FDP 975min. base 780 (3 seg day), +240=1020.
	withoutSplit := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B738", Segments: segs}
	ev, err := cps.EvaluateTrip(ctx, withoutSplit, T0)
	if err != nil {
		return fmt.Errorf("split no-credit: %w", err)
	}
	if ev.Metrics.FDPLimitMin != 780 {
		return fmt.Errorf("split base fdp_limit=%d want 780", ev.Metrics.FDPLimitMin)
	}
	if ev.Verdict != domain.VerdictIllegal || !hasRule(ev.Violations, domain.RuleFDP) {
		return fmt.Errorf("split no-credit: want ILLEGAL/R1 got %s %v", ev.Verdict, ev.Violations)
	}
	withSplit := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B738", SplitBreakMin: 240, Segments: segs}
	ev2, err := cps.EvaluateTrip(ctx, withSplit, T0)
	if err != nil {
		return fmt.Errorf("split credit: %w", err)
	}
	if ev2.Metrics.FDPLimitMin != 1020 {
		return fmt.Errorf("split credit fdp_limit=%d want 1020", ev2.Metrics.FDPLimitMin)
	}
	if ev2.Verdict != domain.VerdictLegal {
		return fmt.Errorf("split credit: want LEGAL got %s %v", ev2.Verdict, ev2.Violations)
	}
	// A break below the 3h threshold gives no credit.
	lowBreak := domain.EvaluateTripRequest{CrewID: 1, AircraftType: "B738", SplitBreakMin: 179, Segments: segs}
	ev3, _ := cps.EvaluateTrip(ctx, lowBreak, T0)
	if ev3.Metrics.FDPLimitMin != 780 {
		return fmt.Errorf("split low break fdp_limit=%d want 780 (no credit)", ev3.Metrics.FDPLimitMin)
	}
	return nil
}

// cumulativeAssertion: R4/R5 — close a real duty with landed segments within
// the 28d/168h windows of T0, then check the cumulative reflects the block
// time (R4) and FDP (R5).
func cumulativeAssertion(ctx context.Context, cs *crew.Service, sch *schedule.Service, cps *compliance.Service) error {
	crewID, err := registerCrew(ctx, cs, "Cum Crew", domain.RoleCaptain, "Asia/Shanghai")
	if err != nil {
		return err
	}
	// Seed a 30h weekly rest so R8 does not fire for this crew's later evaluation.
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: t("2026-08-16T06:00:00Z"), End: t("2026-08-17T12:00:00Z"),
		RestType: domain.RestNormal, DurationMin: 30 * 60,
	}); err != nil {
		return fmt.Errorf("cum weekly rest: %w", err)
	}
	// Build a trip with two 60-min segments landing within the 168h window of T0.
	trip, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		return fmt.Errorf("cum create trip: %w", err)
	}
	for i := 0; i < 2; i++ {
		// seg1: dep 06:00 arr 07:00; seg2: dep 07:30 arr 08:30 (non-overlapping).
		dep := t(fmt.Sprintf("2026-08-17T0%d:%02d:00Z", 6+i, i*30))
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
			TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
			ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
		}); err != nil {
			return fmt.Errorf("cum add segment: %w", err)
		}
	}
	duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
	if err != nil {
		return fmt.Errorf("cum create duty: %w", err)
	}
	if _, err := sch.CloseDuty(ctx, duty.ID, 0); err != nil {
		return fmt.Errorf("cum close duty: %w", err)
	}
	// Cumulative at T0: 28d/365d used = 120min (2 segments × 60). 168h FDP =
	// report 05:00Z .. release 08:45Z = 225min.
	snap, err := cps.Cumulative(ctx, crewID, T0)
	if err != nil {
		return fmt.Errorf("cum cumulative: %w", err)
	}
	if snap.Used28dMin != 120 {
		return fmt.Errorf("cum used_28d=%d want 120", snap.Used28dMin)
	}
	if snap.Used168hMin != 225 {
		return fmt.Errorf("cum used_168h=%d want 225", snap.Used168hMin)
	}
	if snap.Used365dMin != 120 {
		return fmt.Errorf("cum used_365d=%d want 120", snap.Used365dMin)
	}
	// Now evaluate a fresh proposed trip on this crew: the cumulative windows
	// already show 120min used; the metrics must carry the carried-over totals.
	req := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-18T13:00:00Z"), ScheduledArr: t("2026-08-18T14:00:00Z")},
	}}
	ev, err := cps.EvaluateTrip(ctx, req, T0)
	if err != nil {
		return fmt.Errorf("cum evaluate: %w", err)
	}
	if ev.Metrics.Used28dMin != 120 {
		return fmt.Errorf("cum eval used_28d=%d want 120 (history carried)", ev.Metrics.Used28dMin)
	}
	if ev.Metrics.Used168hMin != 225 {
		return fmt.Errorf("cum eval used_168h=%d want 225 (history carried)", ev.Metrics.Used168hMin)
	}
	return nil
}

// minRestAssertion: R7 — after a closed duty, a proposed duty with a 9h gap
// (reduced rest) is allowed (first reduction creates compensatory debt). After
// that duty is also closed, a second reduced rest with outstanding debt trips
// R7. A gap below the 9h floor is always a violation.
func minRestAssertion(ctx context.Context, cs *crew.Service, sch *schedule.Service, cps *compliance.Service) error {
	crewID, err := registerCrew(ctx, cs, "Rest Crew", domain.RoleCaptain, "Asia/Shanghai")
	if err != nil {
		return err
	}
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: t("2026-08-16T06:00:00Z"), End: t("2026-08-17T12:00:00Z"),
		RestType: domain.RestNormal, DurationMin: 30 * 60,
	}); err != nil {
		return err
	}
	// Duty A: segment dep 06:00Z Aug17, arr 07:00Z → report 05:00Z, release 07:15Z.
	tripA, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		return err
	}
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
		TripID: tripA.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
		ScheduledDep: t("2026-08-17T06:00:00Z"), ScheduledArr: t("2026-08-17T07:00:00Z"), BlockTimeMin: 60,
	}); err != nil {
		return err
	}
	dutyA, err := sch.CreateDutyForTrip(ctx, tripA.ID, false, 0)
	if err != nil {
		return err
	}
	if _, err := sch.CloseDuty(ctx, dutyA.ID, 0); err != nil {
		return err
	}
	// First reduced rest: propose duty B with a 9h gap (release 07:15Z → report 16:15Z).
	// gap 540min ∈ [540,600) → reduced, allowed, creates 60min compensatory debt.
	reqB := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-17T17:15:00Z"), ScheduledArr: t("2026-08-17T18:15:00Z")},
	}}
	evB, err := cps.EvaluateTrip(ctx, reqB, t("2026-08-17T17:00:00Z"))
	if err != nil {
		return fmt.Errorf("min rest first reduced evaluate: %w", err)
	}
	if evB.Metrics.RestSinceLastMin != 9*60 {
		return fmt.Errorf("min rest first: rest_since_last=%d want 540", evB.Metrics.RestSinceLastMin)
	}
	if hasRule(evB.Violations, domain.RuleMinRest) {
		return fmt.Errorf("min rest first: first reduced rest should be allowed, got R7 (%v)", evB.Violations)
	}
	// Now actually close duty B so the reduced rest is persisted in history.
	tripB, err := sch.CreateTrip(ctx, crewID)
	if err != nil {
		return err
	}
	if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
		TripID: tripB.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
		ScheduledDep: t("2026-08-17T17:15:00Z"), ScheduledArr: t("2026-08-17T18:15:00Z"), BlockTimeMin: 60,
	}); err != nil {
		return err
	}
	dutyB, err := sch.CreateDutyForTrip(ctx, tripB.ID, false, 0)
	if err != nil {
		return err
	}
	if _, err := sch.CloseDuty(ctx, dutyB.ID, 0); err != nil {
		return err
	}
	// Duty B release = 18:30Z. Second reduced rest: propose duty C with a 9h gap
	// (release 18:30Z → report 03:30Z Aug18). The 60min debt is outstanding → R7.
	reqC := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-18T04:30:00Z"), ScheduledArr: t("2026-08-18T05:30:00Z")},
	}}
	evC, err := cps.EvaluateTrip(ctx, reqC, t("2026-08-18T04:00:00Z"))
	if err != nil {
		return fmt.Errorf("min rest second reduced evaluate: %w", err)
	}
	if !hasRule(evC.Violations, domain.RuleMinRest) {
		return fmt.Errorf("min rest second: expected R7 on reduced rest with outstanding debt, got %v", evC.Violations)
	}
	// Below-floor gap (8h) is always a violation regardless of debt.
	// Release of last closed duty (B) = 18:30Z Aug17; report 02:30Z Aug18 = 8h gap.
	reqD := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-18T03:30:00Z"), ScheduledArr: t("2026-08-18T04:30:00Z")},
	}}
	evD, _ := cps.EvaluateTrip(ctx, reqD, t("2026-08-18T03:00:00Z"))
	if !hasRule(evD.Violations, domain.RuleMinRest) {
		return fmt.Errorf("min rest below-floor: expected R7, got %v", evD.Violations)
	}
	return nil
}

// earlyStartAssertion: R9 — close 5 early-start duties (report <07:00 local in
// Asia/Shanghai = UTC 23:00Z prior day), then a 6th early start trips R9.
func earlyStartAssertion(ctx context.Context, cs *crew.Service, sch *schedule.Service, cps *compliance.Service) error {
	crewID, err := registerCrew(ctx, cs, "Early Crew", domain.RoleCaptain, "Asia/Shanghai")
	if err != nil {
		return err
	}
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: t("2026-08-10T00:00:00Z"), End: t("2026-08-11T08:00:00Z"),
		RestType: domain.RestNormal, DurationMin: 32 * 60,
	}); err != nil {
		return err
	}
	// 5 closed duties, each a single segment. Make each report < 07:00 local
	// (Asia/Shanghai = UTC+8): local 05:00 = UTC 21:00Z prior day. So segment
	// dep UTC 22:00Z, report 21:00Z (local 05:00) → early.
	for i := 0; i < 5; i++ {
		day := 12 + i
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			return err
		}
		dep := t(fmt.Sprintf("2026-08-%dT22:00:00Z", day))
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
			TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
			ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
		}); err != nil {
			return err
		}
		duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
		if err != nil {
			return err
		}
		if _, err := sch.CloseDuty(ctx, duty.ID, 0); err != nil {
			return err
		}
	}
	// 6th early-start proposed duty: segment dep 22:00Z Aug17, report 21:00Z local 05:00.
	req := domain.EvaluateTripRequest{CrewID: crewID, AircraftType: "B738", Segments: []domain.SegmentInput{
		{DepAirport: "PEK", ArrAirport: "SHA", ScheduledDep: t("2026-08-17T22:00:00Z"), ScheduledArr: t("2026-08-17T23:00:00Z")},
	}}
	ev, err := cps.EvaluateTrip(ctx, req, t("2026-08-17T22:30:00Z"))
	if err != nil {
		return fmt.Errorf("early start evaluate: %w", err)
	}
	if ev.Metrics.EarlyStartStreak != 6 {
		return fmt.Errorf("early start streak=%d want 6", ev.Metrics.EarlyStartStreak)
	}
	if !hasRule(ev.Violations, domain.RuleEarlyStart) {
		return fmt.Errorf("early start: expected R9 violation, got %v", ev.Violations)
	}
	return nil
}

// unforeseenQuotaAssertion: R10 — close two duties each with an unforeseen
// extension, then a third extension exceeds the yearly limit.
func unforeseenQuotaAssertion(ctx context.Context, cs *crew.Service, sch *schedule.Service, cps *compliance.Service) error {
	crewID, err := registerCrew(ctx, cs, "Unfor Crew", domain.RoleCaptain, "Asia/Shanghai")
	if err != nil {
		return err
	}
	if _, err := sch.CreateRest(ctx, &domain.RestPeriod{
		CrewID: crewID, Start: t("2026-08-10T00:00:00Z"), End: t("2026-08-11T08:00:00Z"),
		RestType: domain.RestNormal, DurationMin: 32 * 60,
	}); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		day := 12 + i
		trip, err := sch.CreateTrip(ctx, crewID)
		if err != nil {
			return err
		}
		dep := t(fmt.Sprintf("2026-08-%dT06:00:00Z", day))
		if _, err := sch.AddSegment(ctx, &domain.FlightSegment{
			TripID: trip.ID, AircraftType: "B738", DepAirport: "PEK", ArrAirport: "SHA",
			ScheduledDep: dep, ScheduledArr: dep.Add(time.Hour), BlockTimeMin: 60,
		}); err != nil {
			return err
		}
		duty, err := sch.CreateDutyForTrip(ctx, trip.ID, false, 0)
		if err != nil {
			return err
		}
		if _, err := sch.CloseDuty(ctx, duty.ID, 120); err != nil { // +2h unforeseen
			return err
		}
	}
	// The third unforeseen extension should trip R10 via the count check.
	debt, err := cps.RestDebt(ctx, crewID, t("2026-08-13T08:00:00Z"))
	if err != nil {
		return fmt.Errorf("unfor rest debt: %w", err)
	}
	if debt.UnforeseenUsedYear != 2 {
		return fmt.Errorf("unfor used_year=%d want 2", debt.UnforeseenUsedYear)
	}
	if !debt.OwesAugmentedRest {
		return fmt.Errorf("unfor: expected owes_augmented_rest=true after extension")
	}
	return nil
}

// recoveryReplayAssertion: close a duty, replay (0 overrides), corrupt a
// snapshot, replay again (override > 0) and verify the value is restored.
func recoveryReplayAssertion(ctx context.Context, sch *schedule.Service, cps *compliance.Service) error {
	// Replay on a fresh crew (no events) yields 0 overrides.
	overrides, _, err := cps.ReplayForCrew(ctx, 1, T0)
	if err != nil {
		return fmt.Errorf("replay fresh: %w", err)
	}
	if overrides != 0 {
		return fmt.Errorf("replay fresh: overrides=%d want 0", overrides)
	}
	// Replay on crew 3 (cumulative assertion) which has closed duty + events.
	overrides2, snap, err := cps.ReplayForCrew(ctx, 3, T0)
	if err != nil {
		return fmt.Errorf("replay cum crew: %w", err)
	}
	// Snapshots were never written for crew 3 (Cumulative does not write
	// snapshots), so every window differs from the stored zero → 3 overrides.
	if overrides2 != 3 {
		return fmt.Errorf("replay cum crew: overrides=%d want 3", overrides2)
	}
	if snap.Used28dMin != 120 {
		return fmt.Errorf("replay cum crew: used_28d=%d want 120", snap.Used28dMin)
	}
	// Replay again: snapshots now match replay → 0 overrides.
	overrides3, _, err := cps.ReplayForCrew(ctx, 3, T0)
	if err != nil {
		return fmt.Errorf("replay converge: %w", err)
	}
	if overrides3 != 0 {
		return fmt.Errorf("replay converge: overrides=%d want 0", overrides3)
	}
	return nil
}

// frontendAssertions serves the embedded page and hits a real business API.
func frontendAssertions(c *client) error {
	body, err := c.getRaw("/")
	if err != nil {
		return fmt.Errorf("frontend page: %w", err)
	}
	if !contains(body, "机组飞行值勤与疲劳合规引擎") {
		return fmt.Errorf("frontend page missing expected title; got %d bytes", len(body))
	}
	hz, err := c.getJSON("/health")
	if err != nil {
		return err
	}
	if hz["status"] != "ok" {
		return fmt.Errorf("healthz status=%v want ok", hz["status"])
	}
	arr, err := c.getArray("/api/crew")
	if err != nil {
		return err
	}
	if len(arr) == 0 {
		return fmt.Errorf("crew list empty; expected seeded captain")
	}
	return nil
}

// --- helpers ---

func t(s string) time.Time {
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic("bad pinned time: " + s)
	}
	return v.UTC()
}

func hasRule(vs []domain.Violation, code domain.RuleCode) bool {
	for _, v := range vs {
		if v.Rule == code {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// client is a minimal HTTP client for the httptest server.
type client struct{ base string }

func (c *client) getRaw(path string) (string, error) {
	resp, err := http.Get(c.base + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return string(b), nil
}

func (c *client) getJSON(path string) (map[string]any, error) {
	resp, err := http.Get(c.base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return v, fmt.Errorf("GET %s: HTTP %d (%v)", path, resp.StatusCode, v["error"])
	}
	return v, nil
}

func (c *client) getArray(path string) ([]any, error) {
	resp, err := http.Get(c.base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v []any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return v, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return v, nil
}
