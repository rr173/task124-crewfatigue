// Package httpapi wires the crew/schedule/compliance services to HTTP/JSON
// endpoints. Handlers are plain functions over the services so the same mux
// is shared by the real server, the smoke test and the handler tests. Routes
// use Go 1.22 ServeMux patterns.
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"task124-crewfatigue/internal/compliance"
	"task124-crewfatigue/internal/crew"
	"task124-crewfatigue/internal/domain"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/store"
)

// Version is reported by /health and /version.
const Version = "task124-crewfatigue/v1"

// Services bundles the services the mux wires up.
type Services struct {
	Store      *store.Store
	Crew       *crew.Service
	Schedule   *schedule.Service
	Compliance *compliance.Service
}

// NewMux builds the HTTP handler tree over the given services. webFS serves
// the embedded frontend (nil disables static routes).
func NewMux(svc Services, webFS http.FileSystem) http.Handler {
	mux := http.NewServeMux()

	// --- health / version ---
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /version", handleVersion)

	// --- crew ---
	mux.HandleFunc("POST /api/crew", handleCreateCrew(svc.Crew))
	mux.HandleFunc("GET /api/crew", handleListCrew(svc.Crew))
	mux.HandleFunc("GET /api/crew/{id}", handleGetCrew(svc.Crew))
	mux.HandleFunc("PATCH /api/crew/{id}", handleUpdateCrew(svc.Crew))

	// --- aircraft types ---
	mux.HandleFunc("POST /api/aircraft-types", handleCreateAircraft(svc.Crew))
	mux.HandleFunc("GET /api/aircraft-types", handleListAircraft(svc.Crew))

	// --- trips ---
	mux.HandleFunc("POST /api/trips", handleCreateTrip(svc.Schedule))
	mux.HandleFunc("GET /api/trips", handleListTrips(svc.Schedule))
	mux.HandleFunc("GET /api/trips/{id}", handleGetTrip(svc.Schedule))
	mux.HandleFunc("POST /api/trips/{id}/segments", handleAddSegment(svc.Schedule))
	mux.HandleFunc("GET /api/trips/{id}/segments", handleListSegments(svc.Schedule))

	// --- duty periods ---
	mux.HandleFunc("POST /api/duty-periods", handleCreateDuty(svc.Schedule))
	mux.HandleFunc("GET /api/duty-periods", handleListDuties(svc.Schedule))
	mux.HandleFunc("GET /api/duty-periods/{id}", handleGetDuty(svc.Schedule))
	mux.HandleFunc("POST /api/duty-periods/{id}/close", handleCloseDuty(svc.Schedule))

	// --- rest periods ---
	mux.HandleFunc("POST /api/rest-periods", handleCreateRest(svc.Schedule))
	mux.HandleFunc("GET /api/rest-periods", handleListRest(svc.Schedule))

	// --- compliance ---
	mux.HandleFunc("GET /api/crew/{id}/cumulative", handleCumulative(svc.Compliance))
	mux.HandleFunc("POST /api/crew/{id}/evaluate-trip", handleEvaluateTrip(svc.Compliance))
	mux.HandleFunc("GET /api/crew/{id}/legality/{tripId}", handleLegalityByTrip(svc.Compliance))
	mux.HandleFunc("GET /api/crew/{id}/rest-debt", handleRestDebt(svc.Compliance))
	mux.HandleFunc("POST /api/crew/{id}/compensatory-rest-check", handleCompensatoryCheck(svc.Compliance))

	// --- admin (replay) ---
	mux.HandleFunc("POST /api/admin/replay", handleReplay(svc.Compliance))

	// --- frontend ---
	if webFS != nil {
		mux.Handle("/", http.FileServer(webFS))
	}
	return mux
}

// --- health / version ---

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
}

func handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}

// --- crew handlers ---

func handleCreateCrew(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			Role     string `json:"role"`
			HomeBase string `json:"home_base"`
			HomeTZ   string `json:"home_tz"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c := &domain.CrewMember{Name: req.Name, Role: domain.CrewRole(req.Role), HomeBase: req.HomeBase, HomeTZ: req.HomeTZ}
		id, err := svc.Register(r.Context(), c)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	}
}

func handleListCrew(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		role := q.Get("role")
		var active *bool
		if v := q.Get("active"); v != "" {
			b := v == "true" || v == "1"
			active = &b
		}
		list, err := svc.List(r.Context(), role, active)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func handleGetCrew(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		c, err := svc.Get(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func handleUpdateCrew(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var req struct {
			Name     string `json:"name"`
			HomeBase string `json:"home_base"`
			HomeTZ   string `json:"home_tz"`
			Active   bool   `json:"active"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c, err := svc.Get(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if req.Name != "" {
			c.Name = req.Name
		}
		if req.HomeBase != "" {
			c.HomeBase = req.HomeBase
		}
		if req.HomeTZ != "" {
			c.HomeTZ = req.HomeTZ
		}
		c.Active = req.Active
		if err := svc.Update(r.Context(), c); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

// --- aircraft handlers ---

func handleCreateAircraft(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code              string `json:"code"`
			HasRestFacility   bool   `json:"has_rest_facility"`
			RestFacilityClass string `json:"rest_facility_class"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a := &domain.AircraftType{Code: req.Code, HasRestFacility: req.HasRestFacility, RestFacilityClass: domain.RestFacilityClass(req.RestFacilityClass)}
		if err := svc.RegisterAircraft(r.Context(), a); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, a)
	}
}

func handleListAircraft(svc *crew.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.ListAircraft(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// --- trip handlers ---

func handleCreateTrip(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CrewID int64 `json:"crew_id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		t, err := svc.CreateTrip(r.Context(), req.CrewID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
	}
}

func handleListTrips(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var crewID int64
		if v := r.URL.Query().Get("crew_id"); v != "" {
			crewID, _ = strconv.ParseInt(v, 10, 64)
		}
		list, err := svc.ListTrips(r.Context(), crewID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func handleGetTrip(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		t, err := svc.GetTrip(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func handleAddSegment(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tripID, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var req struct {
			AircraftType  string    `json:"aircraft_type"`
			DepAirport    string    `json:"dep_airport"`
			ArrAirport    string    `json:"arr_airport"`
			ScheduledDep  time.Time `json:"scheduled_dep"`
			ScheduledArr  time.Time `json:"scheduled_arr"`
			BlockTimeMin  int       `json:"block_time_min"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		seg := &domain.FlightSegment{
			TripID: tripID, AircraftType: req.AircraftType, DepAirport: req.DepAirport, ArrAirport: req.ArrAirport,
			ScheduledDep: req.ScheduledDep, ScheduledArr: req.ScheduledArr, BlockTimeMin: req.BlockTimeMin,
		}
		out, err := svc.AddSegment(r.Context(), seg)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	}
}

func handleListSegments(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tripID, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		list, err := svc.ListSegments(r.Context(), tripID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// --- duty handlers ---

func handleCreateDuty(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TripID        int64 `json:"trip_id"`
			IsAugmented   bool  `json:"is_augmented"`
			SplitBreakMin int   `json:"split_break_min"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		d, err := svc.CreateDutyForTrip(r.Context(), req.TripID, req.IsAugmented, req.SplitBreakMin)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, d)
	}
}

func handleListDuties(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var crewID int64
		if v := r.URL.Query().Get("crew_id"); v != "" {
			crewID, _ = strconv.ParseInt(v, 10, 64)
		}
		status := r.URL.Query().Get("status")
		list, err := svc.ListDuties(r.Context(), crewID, status)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func handleGetDuty(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		d, err := svc.GetDuty(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}

func handleCloseDuty(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var req struct {
			UnforeseenMin int `json:"unforeseen_min"`
		}
		// Body optional; ignore decode errors (default 0).
		_ = decodeJSON(r, &req)
		d, err := svc.CloseDuty(r.Context(), id, req.UnforeseenMin)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}

// --- rest handlers ---

func handleCreateRest(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CrewID       int64     `json:"crew_id"`
			Start        time.Time `json:"start"`
			End          time.Time `json:"end"`
			RestType     string    `json:"rest_type"`
			CoversWeekly bool      `json:"covers_weekly"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rp := &domain.RestPeriod{
			CrewID: req.CrewID, Start: req.Start, End: req.End,
			RestType: domain.RestType(req.RestType), CoversWeekly: req.CoversWeekly,
		}
		out, err := svc.CreateRest(r.Context(), rp)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	}
}

func handleListRest(svc *schedule.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var crewID int64
		if v := r.URL.Query().Get("crew_id"); v != "" {
			crewID, _ = strconv.ParseInt(v, 10, 64)
		}
		list, err := svc.ListRest(r.Context(), crewID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// --- compliance handlers ---

func handleCumulative(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var asOf time.Time
		if v := r.URL.Query().Get("as_of"); v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				asOf = t
			}
		}
		snap, err := svc.Cumulative(r.Context(), id, asOf)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snap)
	}
}

func handleEvaluateTrip(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var req struct {
			AircraftType  string               `json:"aircraft_type"`
			IsAugmented   bool                 `json:"is_augmented"`
			SplitBreakMin int                  `json:"split_break_min"`
			Segments      []domain.SegmentInput `json:"segments"`
			AsOf          time.Time            `json:"as_of"`
			ApplyUnforeseen bool              `json:"apply_unforeseen"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in := domain.EvaluateTripRequest{
			CrewID: id, AircraftType: req.AircraftType, IsAugmented: req.IsAugmented,
			SplitBreakMin: req.SplitBreakMin, Segments: req.Segments,
		}
		eval, err := svc.EvaluateTrip(r.Context(), in, req.AsOf)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, eval)
	}
}

func handleLegalityByTrip(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		crewID, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		tripID, ok := pathID(w, r, "tripId")
		if !ok {
			return
		}
		e, err := svc.GetEvaluationByTrip(r.Context(), crewID, tripID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	}
}

func handleRestDebt(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var asOf time.Time
		if v := r.URL.Query().Get("as_of"); v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				asOf = t
			}
		}
		debt, err := svc.RestDebt(r.Context(), id, asOf)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, debt)
	}
}

func handleCompensatoryCheck(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var req struct {
			ProposedRestMin int       `json:"proposed_rest_min"`
			AsOf            time.Time `json:"as_of"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ok2, remaining, err := svc.CompensatoryRestCheck(r.Context(), id, req.ProposedRestMin, req.AsOf)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok2, "remaining_min": remaining})
	}
}

func handleReplay(svc *compliance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CrewID int64     `json:"crew_id"`
			AsOf   time.Time `json:"as_of"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		overrides, snap, err := svc.ReplayForCrew(r.Context(), req.CrewID, req.AsOf)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"overrides": overrides, "snapshot": snap})
	}
}
