// Package domain defines the core business types, enumerated values and
// sentinel errors for the aviation crew flight-duty & fatigue compliance
// engine. It is a leaf package: it depends only on the standard library so
// every other package (storage, fatigue rules, compliance, httpapi) can share
// these types without import cycles.
package domain

import "time"

// CrewRole identifies a crew member's duty role on a flight deck.
type CrewRole string

const (
	RoleCaptain      CrewRole = "CAPTAIN"
	RoleFirstOfficer CrewRole = "FIRST_OFFICER"
	RoleReliefPilot  CrewRole = "RELIEF_PILOT"
)

// IsValid reports whether r is a known crew role.
func (r CrewRole) IsValid() bool {
	switch r {
	case RoleCaptain, RoleFirstOfficer, RoleReliefPilot:
		return true
	}
	return false
}

// RestFacilityClass grades the in-flight rest facility available on an aircraft
// type. It drives the augmented-crew FDP extension (R2): CLASS_1 (bunk) gives
// the largest extension, CLASS_3 (ordinary seat) the smallest, NONE none.
type RestFacilityClass string

const (
	FacilityNone   RestFacilityClass = "NONE"
	FacilityClass3 RestFacilityClass = "CLASS_3"
	FacilityClass2 RestFacilityClass = "CLASS_2"
	FacilityClass1 RestFacilityClass = "CLASS_1"
)

// IsValid reports whether c is a known rest facility class.
func (c RestFacilityClass) IsValid() bool {
	switch c {
	case FacilityNone, FacilityClass3, FacilityClass2, FacilityClass1:
		return true
	}
	return false
}

// ExtensionHours returns the FDP extension (in hours) granted by this
// facility class for an augmented duty period. NONE returns 0.
func (c RestFacilityClass) ExtensionHours() int {
	switch c {
	case FacilityClass1:
		return 4
	case FacilityClass2:
		return 3
	case FacilityClass3:
		return 2
	}
	return 0
}

// DutyStatus is the state of a duty period.
type DutyStatus string

const (
	DutyOpen   DutyStatus = "OPEN"
	DutyClosed DutyStatus = "CLOSED"
)

// RestType classifies a rest period.
type RestType string

const (
	RestNormal        RestType = "NORMAL"
	RestAugmented     RestType = "AUGMENTED"
	RestSplitCredit   RestType = "SPLIT_CREDIT"
	RestCompensatory  RestType = "COMPENSATORY"
)

// IsValid reports whether t is a known rest type.
func (t RestType) IsValid() bool {
	switch t {
	case RestNormal, RestAugmented, RestSplitCredit, RestCompensatory:
		return true
	}
	return false
}

// EventKind is the type of a compliance event recorded in the append-only log.
type EventKind string

const (
	EventSegmentLanded      EventKind = "SEGMENT_LANDED"
	EventDutyClosed         EventKind = "DUTY_CLOSED"
	EventRestCompleted      EventKind = "REST_COMPLETED"
	EventUnforeseenExtended EventKind = "UNFORESEEN_EXTENSION"
	EventEvaluation         EventKind = "EVALUATION"
	EventReplayOverride     EventKind = "REPLAY_OVERRIDE"
)

// Verdict is the outcome of a legality evaluation.
type Verdict string

const (
	VerdictLegal   Verdict = "LEGAL"
	VerdictIllegal Verdict = "ILLEGAL"
)

// RuleCode identifies a fatigue rule for violation reporting.
type RuleCode string

const (
	RuleFDP               RuleCode = "R1_FDP_LIMIT"
	RuleAugmented        RuleCode = "R2_AUGMENTED_EXTENSION"
	RuleSplitDuty        RuleCode = "R3_SPLIT_DUTY"
	RuleCum28d           RuleCode = "R4_28D_FLIGHT_TIME"
	RuleCum168h          RuleCode = "R5_168H_FDP"
	RuleCum365d          RuleCode = "R6_365D_FLIGHT_TIME"
	RuleMinRest          RuleCode = "R7_MIN_REST"
	RuleWeeklyRest       RuleCode = "R8_WEEKLY_REST"
	RuleEarlyStart       RuleCode = "R9_EARLY_START_STREAK"
	RuleUnforeseenExtend RuleCode = "R10_UNFORESEEN_EXTENSION"
)

// CrewMember is a pilot or relief pilot who can be assigned to trips.
type CrewMember struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Role      CrewRole  `json:"role"`
	HomeBase  string    `json:"home_base"`
	HomeTZ    string    `json:"home_tz"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// AircraftType is a flyable type with an optional rest facility.
type AircraftType struct {
	Code               string             `json:"code"`
	HasRestFacility    bool               `json:"has_rest_facility"`
	RestFacilityClass  RestFacilityClass  `json:"rest_facility_class"`
}

// Trip groups a sequence of flight segments for one crew member.
type Trip struct {
	ID        int64     `json:"id"`
	CrewID    int64     `json:"crew_id"`
	Planned   bool      `json:"planned"`
	CreatedAt time.Time `json:"created_at"`
}

// FlightSegment is one leg of a trip.
type FlightSegment struct {
	ID            int64     `json:"id"`
	TripID        int64     `json:"trip_id"`
	AircraftType   string    `json:"aircraft_type"`
	DepAirport     string    `json:"dep_airport"`
	ArrAirport     string    `json:"arr_airport"`
	ScheduledDep   time.Time `json:"scheduled_dep"`
	ScheduledArr   time.Time `json:"scheduled_arr"`
	ActualDep      time.Time `json:"actual_dep,omitempty"`
	ActualArr      time.Time `json:"actual_arr,omitempty"`
	BlockTimeMin   int       `json:"block_time_min"`
}

// DutyPeriod wraps one or more consecutive segments with a report/release
// time and optional augmented-crew / split-duty metadata.
type DutyPeriod struct {
	ID                    int64           `json:"id"`
	CrewID                int64           `json:"crew_id"`
	TripID                int64           `json:"trip_id"`
	ReportTime            time.Time       `json:"report_time"`
	ReleaseTime           time.Time       `json:"release_time"`
	IsAugmented           bool            `json:"is_augmented"`
	SplitBreakMin         int             `json:"split_break_min"`
	Status                DutyStatus      `json:"status"`
	UnforeseenExtensionMin int             `json:"unforeseen_extension_min"`
	OweAugmentedRest      bool            `json:"owe_augmented_rest"`
	Segments              []FlightSegment `json:"segments"`
}

// FDP returns the flight duty period duration: release − report.
func (d *DutyPeriod) FDP() time.Duration {
	return d.ReleaseTime.Sub(d.ReportTime)
}

// RestPeriod is a block of crew rest between duty periods.
type RestPeriod struct {
	ID            int64     `json:"id"`
	CrewID        int64     `json:"crew_id"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	RestType      RestType  `json:"rest_type"`
	DurationMin   int       `json:"duration_min"`
	CoversWeekly  bool      `json:"covers_weekly"`
}

// Duration returns the rest duration.
func (r *RestPeriod) Duration() time.Duration {
	return r.End.Sub(r.Start)
}

// ComplianceEvent is the append-only fact log used to rebuild cumulative state.
type ComplianceEvent struct {
	ID          int64     `json:"id"`
	CrewID      int64     `json:"crew_id"`
	Ts          time.Time `json:"ts"`
	Kind        EventKind `json:"kind"`
	PayloadJSON string    `json:"payload_json"`
}

// Violation is one rule breach found during evaluation.
type Violation struct {
	Rule    RuleCode `json:"rule"`
	Message string   `json:"message"`
	Actual  string   `json:"actual"`
	Limit   string   `json:"limit"`
}

// EvaluationMetrics carries the numeric detail behind a verdict.
type EvaluationMetrics struct {
	FDPMin            int   `json:"fdp_min"`
	FDPLimitMin       int   `json:"fdp_limit_min"`
	Used28dMin        int   `json:"used_28d_min"`
	Limit28dMin       int   `json:"limit_28d_min"`
	Used168hMin       int   `json:"used_168h_min"`
	Limit168hMin      int   `json:"limit_168h_min"`
	Used365dMin       int   `json:"used_365d_min"`
	Limit365dMin      int   `json:"limit_365d_min"`
	RestSinceLastMin  int   `json:"rest_since_last_min"`
	MinRestMin        int   `json:"min_rest_min"`
	EarlyStartStreak  int   `json:"early_start_streak"`
	EarlyStartLimit   int   `json:"early_start_limit"`
	UnforeseenUsedYear int  `json:"unforeseen_used_year"`
	UnforeseenLimitYear int  `json:"unforeseen_limit_year"`
}

// LegalityEvaluation is the persisted result of evaluating a trip.
type LegalityEvaluation struct {
	ID          int64              `json:"id"`
	CrewID      int64              `json:"crew_id"`
	TripID      int64              `json:"trip_id"`
	EvaluatedAt time.Time          `json:"evaluated_at"`
	Verdict     Verdict            `json:"verdict"`
	Violations  []Violation        `json:"violations"`
	Metrics     EvaluationMetrics  `json:"metrics"`
}

// CumulativeSnapshot is the derived rolling-window usage for a crew member at
// a given instant. It is rebuilt from the event log on restart (replay wins).
type CumulativeSnapshot struct {
	CrewID      int64     `json:"crew_id"`
	AsOf        time.Time `json:"as_of"`
	Used28dMin  int       `json:"used_28d_min"`
	Used168hMin int       `json:"used_168h_min"`
	Used365dMin int       `json:"used_365d_min"`
}

// RestDebt summarizes a crew member's outstanding rest obligations.
type RestDebt struct {
	CrewID                    int64   `json:"crew_id"`
	AsOf                      time.Time `json:"as_of"`
	CompensatoryOwedMin       int     `json:"compensatory_owed_min"`
	CompensatoryDueBy          *time.Time `json:"compensatory_due_by,omitempty"`
	OwesWeeklyRest             bool    `json:"owes_weekly_rest"`
	OwesAugmentedRest          bool    `json:"owes_augmented_rest"`
	EarlyStartStreak          int     `json:"early_start_streak"`
	UnforeseenUsedYear        int     `json:"unforeseen_used_year"`
}

// EvaluateTripRequest is the input to an in-process trip evaluation.
type EvaluateTripRequest struct {
	CrewID         int64           `json:"crew_id"`
	AircraftType    string          `json:"aircraft_type"`
	IsAugmented     bool            `json:"is_augmented"`
	SplitBreakMin   int             `json:"split_break_min"`
	Segments        []SegmentInput  `json:"segments"`
}

// SegmentInput is a planned segment for evaluation (times only).
type SegmentInput struct {
	DepAirport    string    `json:"dep_airport"`
	ArrAirport    string    `json:"arr_airport"`
	ScheduledDep  time.Time `json:"scheduled_dep"`
	ScheduledArr  time.Time `json:"scheduled_arr"`
}

// LegalityResult is the output of the fatigue rule evaluation.
type LegalityResult struct {
	Verdict    Verdict            `json:"verdict"`
	Violations []Violation        `json:"violations"`
	Metrics    EvaluationMetrics  `json:"metrics"`
}
