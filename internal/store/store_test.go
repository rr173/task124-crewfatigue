package store

import (
	"context"
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCrewCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := &domain.CrewMember{Name: "Li", Role: domain.RoleCaptain, HomeBase: "PEK", HomeTZ: "Asia/Shanghai", Active: true}
	id, err := CreateCrew(ctx, st.DB(), c)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("id zero")
	}
	got, err := GetCrew(ctx, st.DB(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Li" || got.Role != domain.RoleCaptain || !got.Active {
		t.Errorf("crew mismatch: %+v", got)
	}
	dup, err := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "Li", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	if err == nil {
		t.Fatalf("expected duplicate error, got id %d", dup)
	}
}

func TestCrewInvalidRoleTZ(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "X", Role: "BOGUS", HomeTZ: "UTC"}); err == nil {
		t.Fatal("expected role error")
	}
	if _, err := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "X", Role: domain.RoleCaptain, HomeTZ: "Not/A/Zone"}); err == nil {
		t.Fatal("expected tz error")
	}
}

func TestAircraftCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone}
	if err := CreateAircraft(ctx, st.DB(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := GetAircraft(ctx, st.DB(), "B738")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HasRestFacility {
		t.Error("B738 should not have rest facility")
	}
	if got.RestFacilityClass != domain.FacilityNone {
		t.Errorf("class=%v want NONE", got.RestFacilityClass)
	}
	if err := CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone}); err == nil {
		t.Fatal("expected duplicate aircraft error")
	}
}

func TestTripSegmentDutyRest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	crewID, _ := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "T", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone})
	tripID, _ := CreateTrip(ctx, st.DB(), &domain.Trip{CrewID: crewID, Planned: true})
	seg := &domain.FlightSegment{
		TripID: tripID, AircraftType: "B738", DepAirport: "A", ArrAirport: "B",
		ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC),
	}
	if _, err := CreateSegment(ctx, st.DB(), seg); err != nil {
		t.Fatalf("segment: %v", err)
	}
	duty := &domain.DutyPeriod{
		CrewID: crewID, TripID: tripID,
		ReportTime:  seg.ScheduledDep.Add(-domain.ReportLeadMin * time.Minute),
		ReleaseTime: seg.ScheduledArr.Add(domain.ReleaseLagMin * time.Minute),
		Status:      domain.DutyOpen,
	}
	dutyID, err := CreateDuty(ctx, st.DB(), duty, []int64{seg.ID})
	if err != nil {
		t.Fatalf("duty: %v", err)
	}
	loaded, err := GetDuty(ctx, st.DB(), dutyID)
	if err != nil {
		t.Fatalf("get duty: %v", err)
	}
	if len(loaded.Segments) != 1 {
		t.Fatalf("segments=%d want 1", len(loaded.Segments))
	}
	if loaded.Status != domain.DutyOpen {
		t.Errorf("status=%v want OPEN", loaded.Status)
	}
	// Close.
	if err := CloseDuty(ctx, st.DB(), dutyID, 0, false); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := CloseDuty(ctx, st.DB(), dutyID, 0, false); err != domain.ErrDutyNotOpen {
		t.Errorf("second close: want ErrDutyNotOpen got %v", err)
	}
	// Rest.
	r := &domain.RestPeriod{CrewID: crewID, Start: time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC), RestType: domain.RestNormal}
	rid, err := CreateRest(ctx, st.DB(), r)
	if err != nil {
		t.Fatalf("rest: %v", err)
	}
	if rid == 0 {
		t.Fatal("rest id zero")
	}
}

func TestSegmentValidation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	crewID, _ := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "V", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	CreateAircraft(ctx, st.DB(), &domain.AircraftType{Code: "B738", RestFacilityClass: domain.FacilityNone})
	tripID, _ := CreateTrip(ctx, st.DB(), &domain.Trip{CrewID: crewID, Planned: true})
	// arr before dep
	bad := &domain.FlightSegment{TripID: tripID, AircraftType: "B738",
		ScheduledDep: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)}
	if _, err := CreateSegment(ctx, st.DB(), bad); err == nil {
		t.Fatal("expected arr-before-dep error")
	}
	// unknown aircraft
	bad2 := &domain.FlightSegment{TripID: tripID, AircraftType: "ZZZ",
		ScheduledDep: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
		ScheduledArr: time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)}
	if _, err := CreateSegment(ctx, st.DB(), bad2); err == nil {
		t.Fatal("expected FK error for unknown aircraft")
	}
}

func TestEventAppendReplay(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	crewID, _ := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "E", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	ts := time.Date(2026, 8, 17, 6, 0, 0, 0, time.UTC)
	ev := &domain.ComplianceEvent{CrewID: crewID, Ts: ts, Kind: domain.EventSegmentLanded, PayloadJSON: `{"segment_id":1,"block_time_min":120,"trip_id":1}`}
	if _, err := AppendEvent(ctx, st.DB(), ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	list, err := ListEventsForCrew(ctx, st.DB(), crewID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("events=%d want 1", len(list))
	}
	if list[0].Kind != domain.EventSegmentLanded {
		t.Errorf("kind=%v want SEGMENT_LANDED", list[0].Kind)
	}
	n, err := CountEventsKindYear(ctx, st.DB(), crewID, domain.EventSegmentLanded, ts)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("count=%d want 1", n)
	}
	// Different year counts zero.
	n2, _ := CountEventsKindYear(ctx, st.DB(), crewID, domain.EventSegmentLanded, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if n2 != 0 {
		t.Errorf("2027 count=%d want 0", n2)
	}
}

func TestSnapshotUpsert(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	crewID, _ := CreateCrew(ctx, st.DB(), &domain.CrewMember{Name: "S", Role: domain.RoleCaptain, HomeTZ: "UTC"})
	asOf := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	if err := PutSnapshot(ctx, st.DB(), crewID, WindowKind28d, asOf, 100); err != nil {
		t.Fatalf("put: %v", err)
	}
	_, n, ok, err := GetSnapshot(ctx, st.DB(), crewID, WindowKind28d)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if n != 100 {
		t.Errorf("snapshot=%d want 100", n)
	}
	if err := PutSnapshot(ctx, st.DB(), crewID, WindowKind28d, asOf, 250); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_, n2, _, _ := GetSnapshot(ctx, st.DB(), crewID, WindowKind28d)
	if n2 != 250 {
		t.Errorf("upsert snapshot=%d want 250", n2)
	}
}
