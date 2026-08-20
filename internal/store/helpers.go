package store

import (
	"database/sql"
	"errors"
	"strings"

	"task124-crewfatigue/internal/domain"
)

// scanner is satisfied by both *sql.Row and *sql.Rows, so scan helpers can
// share one implementation across Get (single row) and List (multi row).
type scanner interface {
	Scan(dest ...any) error
}

// mapErr translates raw driver errors into domain sentinel errors. It is the
// single place that maps "no rows" and UNIQUE-constraint failures, so adding
// a new entity only needs a new contains() check for its unique columns.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if err == sql.ErrNoRows {
		return domain.ErrCrewNotFound
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "UNIQUE constraint failed: crew_members"):
		return fmtWrap(domain.ErrCrewExists, err)
	case strings.Contains(s, "UNIQUE constraint failed: aircraft_types"):
		return fmtWrap(domain.ErrAircraftExists, err)
	case strings.Contains(s, "FOREIGN KEY constraint failed"):
		return errors.New("foreign key constraint failed: referenced row does not exist")
	}
	return err
}

// fmtWrap returns err with the sentinel prefix so errors.Is keeps working.
func fmtWrap(sentinel, err error) error {
	return errors.Join(sentinel, err)
}

// contains is a tiny strings.Contains alias kept local for symmetry with the
// other store files and to avoid importing strings everywhere.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// isNoRows reports whether err is the driver's "no rows" result. Each
// resource-specific mapXErr helper falls back to this to distinguish a missing
// row from a real driver error (the generic mapErr defaults to crew-not-found,
// so non-crew lookups must map no-rows to their own sentinel).
func isNoRows(err error) bool {
	return err == sql.ErrNoRows
}

// nullTime scans an optional UTC timestamp column. Empty string → zero time.
func nullTime(s sql.NullString) (t string) {
	if s.Valid {
		return s.String
	}
	return ""
}

// boolToInt converts a bool to the integer column encoding (1/0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
