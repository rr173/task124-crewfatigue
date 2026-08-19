// Package crew provides the crew-member and aircraft-type service: registration,
// lookup, update and listing. It validates role/timezone at the boundary and
// delegates persistence to the store layer, always inside a transaction so
// that a failed registration leaves no partial rows.
package crew

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

// Service wraps a *store.Store to provide crew/aircraft operations.
type Service struct {
	st *store.Store
}

// New returns a crew service backed by st.
func New(st *store.Store) *Service { return &Service{st: st} }

// Register creates a crew member and returns its ID.
func (s *Service) Register(ctx context.Context, c *domain.CrewMember) (int64, error) {
	if c.Name == "" {
		return 0, fmt.Errorf("%w: name empty", domain.ErrInvariantViolation)
	}
	if !c.Role.IsValid() {
		return 0, fmt.Errorf("%w: role %q", domain.ErrRoleInvalid, c.Role)
	}
	if c.HomeTZ == "" {
		return 0, fmt.Errorf("%w: home_tz empty", domain.ErrTZInvalid)
	}
	if _, err := time.LoadLocation(c.HomeTZ); err != nil {
		return 0, fmt.Errorf("%w: %v", domain.ErrTZInvalid, err)
	}
	if c.HomeBase == "" {
		c.HomeBase = "N/A"
	}
	c.Active = true
	var id int64
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		rid, err := store.CreateCrew(ctx, tx, c)
		if err != nil {
			return err
		}
		id = rid
		return nil
	})
	return id, err
}

// Get returns a crew member by ID.
func (s *Service) Get(ctx context.Context, id int64) (*domain.CrewMember, error) {
	var c *domain.CrewMember
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetCrew(ctx, tx, id)
		if err != nil {
			return err
		}
		c = got
		return nil
	})
	return c, err
}

// List returns crew members, optionally filtered by role and/or active flag.
func (s *Service) List(ctx context.Context, role string, active *bool) ([]*domain.CrewMember, error) {
	var out []*domain.CrewMember
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListCrew(ctx, tx, role, active)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// Update patches mutable crew fields (name, home_base, home_tz, active).
func (s *Service) Update(ctx context.Context, c *domain.CrewMember) error {
	if c.ID <= 0 {
		return fmt.Errorf("%w: id must be positive", domain.ErrInvariantViolation)
	}
	return s.st.InTx(ctx, func(tx store.DBTX) error {
		return store.UpdateCrew(ctx, tx, c)
	})
}

// RegisterAircraft creates an aircraft type.
func (s *Service) RegisterAircraft(ctx context.Context, a *domain.AircraftType) error {
	if a.Code == "" {
		return fmt.Errorf("%w: code empty", domain.ErrInvariantViolation)
	}
	if !a.RestFacilityClass.IsValid() {
		return fmt.Errorf("%w: class %q", domain.ErrFacilityInvalid, a.RestFacilityClass)
	}
	return s.st.InTx(ctx, func(tx store.DBTX) error {
		return store.CreateAircraft(ctx, tx, a)
	})
}

// GetAircraft returns an aircraft type by code.
func (s *Service) GetAircraft(ctx context.Context, code string) (*domain.AircraftType, error) {
	var a *domain.AircraftType
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetAircraft(ctx, tx, code)
		if err != nil {
			return err
		}
		a = got
		return nil
	})
	return a, err
}

// ListAircraft returns all aircraft types.
func (s *Service) ListAircraft(ctx context.Context) ([]*domain.AircraftType, error) {
	var out []*domain.AircraftType
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListAircraft(ctx, tx)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}
