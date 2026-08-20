package store

import (
	"context"
	"fmt"

	"task124-crewfatigue/internal/domain"
)

// CreateAircraft inserts an aircraft type. code is the primary key.
func CreateAircraft(ctx context.Context, tx DBTX, a *domain.AircraftType) error {
	if !a.RestFacilityClass.IsValid() {
		return fmt.Errorf("%w: class %q", domain.ErrFacilityInvalid, a.RestFacilityClass)
	}
	if a.RestFacilityClass != domain.FacilityNone && !a.HasRestFacility {
		// A non-NONE class implies a facility exists; keep them consistent.
		a.HasRestFacility = true
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO aircraft_types
		(code, has_rest_facility, rest_facility_class) VALUES (?,?,?)`,
		a.Code, boolToInt(a.HasRestFacility), string(a.RestFacilityClass))
	return mapErr(err)
}

// GetAircraft loads an aircraft type by code.
func GetAircraft(ctx context.Context, tx DBTX, code string) (*domain.AircraftType, error) {
	row := tx.QueryRowContext(ctx, `SELECT code, has_rest_facility, rest_facility_class FROM aircraft_types WHERE code = ?`, code)
	var a domain.AircraftType
	var has int
	if err := row.Scan(&a.Code, &has, &a.RestFacilityClass); err != nil {
		return nil, mapAircraftErr(err)
	}
	a.HasRestFacility = has != 0
	return &a, nil
}

// ListAircraft lists all aircraft types ordered by code.
func ListAircraft(ctx context.Context, tx DBTX) ([]*domain.AircraftType, error) {
	rows, err := tx.QueryContext(ctx, `SELECT code, has_rest_facility, rest_facility_class FROM aircraft_types ORDER BY code ASC`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []*domain.AircraftType
	for rows.Next() {
		var a domain.AircraftType
		var has int
		if err := rows.Scan(&a.Code, &has, &a.RestFacilityClass); err != nil {
			return nil, mapErr(err)
		}
		a.HasRestFacility = has != 0
		out = append(out, &a)
	}
	return out, rows.Err()
}

// mapAircraftErr maps the no-rows error from an aircraft lookup to the
// aircraft-specific sentinel (mapErr defaults to crew-not-found).
func mapAircraftErr(err error) error {
	if err == nil {
		return nil
	}
	if isNoRows(err) {
		return domain.ErrAircraftNotFound
	}
	return err
}
