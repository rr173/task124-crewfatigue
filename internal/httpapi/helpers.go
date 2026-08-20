package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"task124-crewfatigue/internal/domain"
)

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError writes a JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON decodes r.Body into v. Times are parsed as RFC3339Nano via the
// json default for time.Time, which the stdlib handles natively.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// pathID extracts and parses a {id} path parameter to a positive int64.
func pathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	v := r.PathValue(key)
	if v == "" {
		writeError(w, http.StatusBadRequest, "missing "+key)
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+key)
		return 0, false
	}
	return id, true
}

// writeServiceError maps a domain error to an HTTP status and writes it.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrCrewNotFound),
		errors.Is(err, domain.ErrAircraftNotFound),
		errors.Is(err, domain.ErrTripNotFound),
		errors.Is(err, domain.ErrSegmentNotFound),
		errors.Is(err, domain.ErrDutyNotFound),
		errors.Is(err, domain.ErrRestNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrCrewExists),
		errors.Is(err, domain.ErrAircraftExists),
		errors.Is(err, domain.ErrDutyClosed),
		errors.Is(err, domain.ErrDutyNotOpen),
		errors.Is(err, domain.ErrDutyEmpty):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrRoleInvalid),
		errors.Is(err, domain.ErrFacilityInvalid),
		errors.Is(err, domain.ErrRestTypeInvalid),
		errors.Is(err, domain.ErrTZInvalid),
		errors.Is(err, domain.ErrInvariantViolation):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// parseAsOf parses an RFC3339Nano query parameter, returning zero on absence
// or error (callers treat zero as "now").
func parseAsOf(r *http.Request, key string) time.Time {
	if v := r.URL.Query().Get(key); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
