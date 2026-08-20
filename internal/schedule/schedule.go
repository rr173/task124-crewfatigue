// Package schedule manages trips, flight segments, duty periods and rest
// periods: the structural spine of crew scheduling. It owns the duty-period
// state machine (OPEN -> CLOSED) and derives report/release times from the
// locked offsets. Closing a duty period also appends the SEGMENT_LANDED and
// DUTY_CLOSED events that the compliance layer replays to rebuild cumulative
// state, so the close path is transactional with the event append.
package schedule

import (
	"context"
	"fmt"
	"time"

	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/store"
)

// Service wraps a *store.Store for schedule operations.
type Service struct {
	st *store.Store
}

// New returns a schedule service backed by st.
func New(st *store.Store) *Service { return &Service{st: st} }

// CreateTrip creates a trip for a crew member. The crew must exist (FK).
func (s *Service) CreateTrip(ctx context.Context, crewID int64) (*domain.Trip, error) {
	if crewID <= 0 {
		return nil, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	t := &domain.Trip{CrewID: crewID, Planned: true}
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		id, err := store.CreateTrip(ctx, tx, t)
		if err != nil {
			return err
		}
		t.ID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetTrip returns a trip by ID.
func (s *Service) GetTrip(ctx context.Context, id int64) (*domain.Trip, error) {
	var t *domain.Trip
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetTrip(ctx, tx, id)
		if err != nil {
			return err
		}
		t = got
		return nil
	})
	return t, err
}

// ListTrips lists trips optionally filtered by crew.
func (s *Service) ListTrips(ctx context.Context, crewID int64) ([]*domain.Trip, error) {
	var out []*domain.Trip
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListTrips(ctx, tx, crewID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// AddSegment appends a planned flight segment to a trip. The aircraft type
// must already be registered (FK). The trip must still be planned (open).
func (s *Service) AddSegment(ctx context.Context, seg *domain.FlightSegment) (*domain.FlightSegment, error) {
	if seg == nil || seg.TripID <= 0 {
		return nil, fmt.Errorf("%w: trip_id must be positive", domain.ErrInvariantViolation)
	}
	if seg.AircraftType == "" {
		return nil, fmt.Errorf("%w: aircraft_type empty", domain.ErrInvariantViolation)
	}
	if !seg.ScheduledArr.After(seg.ScheduledDep) {
		return nil, fmt.Errorf("%w: scheduled_arr must be after scheduled_dep", domain.ErrInvariantViolation)
	}
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		t, err := store.GetTrip(ctx, tx, seg.TripID)
		if err != nil {
			return err
		}
		if !t.Planned {
			return domain.ErrDutyClosed
		}
		// Validate aircraft type exists.
		if _, err := store.GetAircraft(ctx, tx, seg.AircraftType); err != nil {
			return err
		}
		id, err := store.CreateSegment(ctx, tx, seg)
		if err != nil {
			return err
		}
		seg.ID = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seg, nil
}

// ListSegments returns the segments of a trip in scheduled-departure order.
func (s *Service) ListSegments(ctx context.Context, tripID int64) ([]*domain.FlightSegment, error) {
	var out []*domain.FlightSegment
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListSegmentsByTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// CreateDutyForTrip builds a duty period from the segments already in a trip
// (in scheduled-departure order) and links them. report_time is first dep -
// ReportLeadMin; release_time is last arr + ReleaseLagMin.
func (s *Service) CreateDutyForTrip(ctx context.Context, tripID int64, isAugmented bool, splitBreakMin int) (*domain.DutyPeriod, error) {
	var d *domain.DutyPeriod
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		t, err := store.GetTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		segs, err := store.ListSegmentsByTrip(ctx, tx, tripID)
		if err != nil {
			return err
		}
		if len(segs) == 0 {
			return domain.ErrDutyEmpty
		}
		if exists, err := store.HasDutyForTrip(ctx, tx, tripID); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("%w: trip already has a duty period", domain.ErrInvariantViolation)
		}
		report := segs[0].ScheduledDep.Add(-domain.ReportLeadMin * time.Minute)
		release := segs[len(segs)-1].ScheduledArr.Add(domain.ReleaseLagMin * time.Minute)
		dp := &domain.DutyPeriod{
			CrewID: t.CrewID, TripID: tripID, ReportTime: report, ReleaseTime: release,
			IsAugmented: isAugmented, SplitBreakMin: splitBreakMin, Status: domain.DutyOpen,
		}
		segIDs := make([]int64, len(segs))
		for i, sg := range segs {
			segIDs[i] = sg.ID
		}
		id, err := store.CreateDuty(ctx, tx, dp, segIDs)
		if err != nil {
			return err
		}
		dp.ID = id
		dp.Segments = toDomainSegs(segs)
		d = dp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// CloseDuty closes a duty period, recording landed segments and the closed
// duty in the compliance event log so the cumulative state can be rebuilt.
// applyUnforeseenMin > 0 requests an R10 unforeseen extension (validated by
// the compliance layer's yearly quota; the close path here only records it).
func (s *Service) CloseDuty(ctx context.Context, dutyID int64, applyUnforeseenMin int) (*domain.DutyPeriod, error) {
	var d *domain.DutyPeriod
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		dp, err := store.GetDuty(ctx, tx, dutyID)
		if err != nil {
			return err
		}
		if dp.Status != domain.DutyOpen {
			return domain.ErrDutyNotOpen
		}
		if len(dp.Segments) == 0 {
			return domain.ErrDutyEmpty
		}
		// Cap the unforeseen extension at the R10 per-duty maximum.
		owe := false
		ext := applyUnforeseenMin
		if ext > domain.UnforeseenExtendMaxMin {
			ext = domain.UnforeseenExtendMaxMin
		}
		if ext > 0 {
			owe = true
		}
		if err := store.CloseDuty(ctx, tx, dutyID, ext, owe); err != nil {
			return err
		}
		// Record SEGMENT_LANDED events for each segment.
		for _, sg := range dp.Segments {
			payload := store.EventPayloadSegment{SegmentID: sg.ID, BlockTimeMin: sg.BlockTimeMin, TripID: dp.TripID}
			pj, _ := encodeJSON(payload)
			ev := &domain.ComplianceEvent{
				CrewID: dp.CrewID, Ts: sg.ScheduledDep.UTC(), Kind: domain.EventSegmentLanded, PayloadJSON: pj,
			}
			if _, err := store.AppendEvent(ctx, tx, ev); err != nil {
				return err
			}
		}
		// Record DUTY_CLOSED event.
		dpayload := store.EventPayloadDuty{
			DutyID: dp.ID, FDPMin: int(dp.FDP().Minutes()),
			UnforeseenExtensionMin: ext, OweAugmentedRest: owe,
		}
		pj, _ := encodeJSON(dpayload)
		dev := &domain.ComplianceEvent{
			CrewID: dp.CrewID, Ts: dp.ReleaseTime.UTC(), Kind: domain.EventDutyClosed, PayloadJSON: pj,
		}
		if _, err := store.AppendEvent(ctx, tx, dev); err != nil {
			return err
		}
		if ext > 0 {
			upayload := store.EventPayloadUnforeseen{DutyID: dp.ID, AddedMin: ext}
			pj, _ := encodeJSON(upayload)
			uev := &domain.ComplianceEvent{
				CrewID: dp.CrewID, Ts: dp.ReleaseTime.UTC(), Kind: domain.EventUnforeseenExtended, PayloadJSON: pj,
			}
			if _, err := store.AppendEvent(ctx, tx, uev); err != nil {
				return err
			}
		}
		// Mark trip unplanned now that its duty is closed.
		if err := store.MarkTripUnplanned(ctx, tx, dp.TripID); err != nil {
			return err
		}
		d, err = store.GetDuty(ctx, tx, dutyID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// GetDuty returns a duty period with its linked segments.
func (s *Service) GetDuty(ctx context.Context, id int64) (*domain.DutyPeriod, error) {
	var d *domain.DutyPeriod
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.GetDuty(ctx, tx, id)
		if err != nil {
			return err
		}
		d = got
		return nil
	})
	return d, err
}

// ListDuties lists duty periods optionally filtered by crew/status.
func (s *Service) ListDuties(ctx context.Context, crewID int64, status string) ([]*domain.DutyPeriod, error) {
	var out []*domain.DutyPeriod
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListDutyPeriods(ctx, tx, crewID, status)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// CreateRest records a rest period and appends a REST_COMPLETED event. A rest
// of at least WeeklyRestHours (or WeeklyRestHomeBaseHours when coversWeekly)
// automatically satisfies R8 for the window it ends in.
func (s *Service) CreateRest(ctx context.Context, r *domain.RestPeriod) (*domain.RestPeriod, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil rest", domain.ErrInvariantViolation)
	}
	if r.CrewID <= 0 {
		return nil, fmt.Errorf("%w: crew_id must be positive", domain.ErrInvariantViolation)
	}
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		id, err := store.CreateRest(ctx, tx, r)
		if err != nil {
			return err
		}
		r.ID = id
		payload := store.EventPayloadRest{
			RestID: r.ID, RestType: string(r.RestType), DurationMin: r.DurationMin,
			CoversWeekly: r.CoversWeekly,
		}
		pj, _ := encodeJSON(payload)
		ev := &domain.ComplianceEvent{
			CrewID: r.CrewID, Ts: r.End.UTC(), Kind: domain.EventRestCompleted, PayloadJSON: pj,
		}
		if _, err := store.AppendEvent(ctx, tx, ev); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRest lists rest periods optionally filtered by crew.
func (s *Service) ListRest(ctx context.Context, crewID int64) ([]*domain.RestPeriod, error) {
	var out []*domain.RestPeriod
	err := s.st.InTx(ctx, func(tx store.DBTX) error {
		got, err := store.ListRest(ctx, tx, crewID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// toDomainSegs converts []*FlightSegment to a value slice (for the
// DutyPeriod.Segments field).
func toDomainSegs(in []*domain.FlightSegment) []domain.FlightSegment {
	out := make([]domain.FlightSegment, len(in))
	for i, s := range in {
		out[i] = *s
	}
	return out
}

// encodeJSON marshals v, returning "" on error (payload best-effort but the
// callers above already structure valid data).
func encodeJSON(v any) (string, error) {
	b, err := jsonMarshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
