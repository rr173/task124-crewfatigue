package crew

import (
	"context"
	"errors"
	"testing"

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

func TestRegisterAndList(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	id, err := svc.Register(ctx, &domain.CrewMember{Name: "A", Role: domain.RoleCaptain, HomeTZ: "Asia/Shanghai"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id == 0 {
		t.Fatal("id zero")
	}
	active := true
	list, err := svc.List(ctx, "CAPTAIN", &active)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "A" {
		t.Errorf("list mismatch: %+v", list)
	}
	// Duplicate (name+role) rejected.
	if _, err := svc.Register(ctx, &domain.CrewMember{Name: "A", Role: domain.RoleCaptain, HomeTZ: "UTC"}); !errors.Is(err, domain.ErrCrewExists) {
		t.Errorf("dup: want ErrCrewExists got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	cases := []struct {
		name string
		c    *domain.CrewMember
	}{
		{"empty name", &domain.CrewMember{Role: domain.RoleCaptain, HomeTZ: "UTC"}},
		{"bad role", &domain.CrewMember{Name: "X", Role: "NOPE", HomeTZ: "UTC"}},
		{"bad tz", &domain.CrewMember{Name: "X", Role: domain.RoleCaptain, HomeTZ: "zzz"}},
	}
	for _, c := range cases {
		if _, err := svc.Register(ctx, c.c); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestUpdateCrew(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	id, _ := svc.Register(ctx, &domain.CrewMember{Name: "U", Role: domain.RoleFirstOfficer, HomeTZ: "UTC"})
	c, _ := svc.Get(ctx, id)
	c.Name = "Updated"
	c.HomeTZ = "Asia/Shanghai"
	if err := svc.Update(ctx, c); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if got.Name != "Updated" || got.HomeTZ != "Asia/Shanghai" {
		t.Errorf("update not applied: %+v", got)
	}
}

func TestAircraftRegisterAndList(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if err := svc.RegisterAircraft(ctx, &domain.AircraftType{Code: "B789", HasRestFacility: true, RestFacilityClass: domain.FacilityClass1}); err != nil {
		t.Fatalf("register: %v", err)
	}
	list, err := svc.ListAircraft(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	if list[0].RestFacilityClass != domain.FacilityClass1 {
		t.Errorf("class=%v want CLASS_1", list[0].RestFacilityClass)
	}
	// Invalid facility class rejected.
	if err := svc.RegisterAircraft(ctx, &domain.AircraftType{Code: "BAD", RestFacilityClass: "WHATEVER"}); !errors.Is(err, domain.ErrFacilityInvalid) {
		t.Errorf("bad class: want ErrFacilityInvalid got %v", err)
	}
	// Duplicate code rejected.
	if err := svc.RegisterAircraft(ctx, &domain.AircraftType{Code: "B789", RestFacilityClass: domain.FacilityNone}); !errors.Is(err, domain.ErrAircraftExists) {
		t.Errorf("dup: want ErrAircraftExists got %v", err)
	}
}
