package domain

import "errors"

// Sentinel errors. The httpapi layer maps these to HTTP statuses; the storage
// and service layers return them verbatim so callers can branch on identity.
var (
	// ErrCrewNotFound is returned when a crew member lookup misses.
	ErrCrewNotFound = errors.New("crew member not found")
	// ErrCrewExists is returned on a duplicate crew (same name + role).
	ErrCrewExists = errors.New("crew member already exists")
	// ErrAircraftNotFound is returned when an aircraft type lookup misses.
	ErrAircraftNotFound = errors.New("aircraft type not found")
	// ErrAircraftExists is returned on a duplicate aircraft code.
	ErrAircraftExists = errors.New("aircraft type already exists")
	// ErrTripNotFound is returned when a trip lookup misses.
	ErrTripNotFound = errors.New("trip not found")
	// ErrSegmentNotFound is returned when a segment lookup misses.
	ErrSegmentNotFound = errors.New("flight segment not found")
	// ErrDutyNotFound is returned when a duty period lookup misses.
	ErrDutyNotFound = errors.New("duty period not found")
	// ErrRestNotFound is returned when a rest period lookup misses.
	ErrRestNotFound = errors.New("rest period not found")

	// ErrDutyClosed is returned when mutating a duty period that is CLOSED.
	ErrDutyClosed = errors.New("duty period is closed")
	// ErrDutyNotOpen is returned when closing a duty period that is not OPEN.
	ErrDutyNotOpen = errors.New("duty period is not open")
	// ErrDutyEmpty is returned when closing a duty period with no segments.
	ErrDutyEmpty = errors.New("duty period has no segments")

	// ErrRoleInvalid is returned for an unknown crew role.
	ErrRoleInvalid = errors.New("invalid crew role")
	// ErrFacilityInvalid is returned for an unknown rest facility class.
	ErrFacilityInvalid = errors.New("invalid rest facility class")
	// ErrRestTypeInvalid is returned for an unknown rest type.
	ErrRestTypeInvalid = errors.New("invalid rest type")
	// ErrTZInvalid is returned for an unknown/invalid IANA timezone.
	ErrTZInvalid = errors.New("invalid home timezone")

	// ErrInvariantViolation is returned when a domain invariant is broken
	// (e.g. negative block time, end before start, empty segment list).
	ErrInvariantViolation = errors.New("invariant violation")
)
