package domain

import "time"

// This file pins the locked fatigue-rule constants (§7 of the design doc).
// They are exported so the fatigue package, compliance package, tests and the
// selfcheck all read the same authoritative numbers. Changing a value here is
// a deliberate spec change.

// ReportLeadMin is the fixed report offset before the first segment's
// scheduled departure: report_time = first_dep - 60min.
const ReportLeadMin = 60

// ReleaseLagMin is the fixed release offset after the last segment's
// scheduled arrival: release_time = last_arr + 15min.
const ReleaseLagMin = 15

// Cumulative limits (rolling windows, all wall-clock based).
const (
	Limit28dFlightHours  = 100 // R4
	Limit168hFDPHours    = 60  // R5
	Limit365dFlightHours = 1000 // R6
)

// Rest limits.
const (
	MinRestHours            = 10 // R7 normal minimum
	MinRestReducedHours     = 9  // R7 unforeseen-reduced floor
	CompensatoryWindowHours = 72 // R7 time to make up a reduced rest
	WeeklyRestHours         = 30 // R8 home-base-away weekly rest
	WeeklyRestHomeBaseHours = 25 // R8 home-base weekly rest
	AugmentedRestHours      = 14 // R10 augmented rest owed after extension
)

// Early-start limits (R9).
const (
	EarlyStartLocalHour = 7 // a report before 07:00 local counts as early
	EarlyStartStreakMax = 5 // at most 5 consecutive early starts
	EarlyStartResetRestHours = 36 // 36h rest resets the streak
)

// Unforeseen-extension limits (R10).
const (
	UnforeseenExtendMaxMin = 120 // +2h per duty period
	UnforeseenYearlyLimit  = 2   // per crew per calendar year
)

// Split-duty credit (R3).
const (
	SplitBreakMinMin  = 180 // a break must be >= 3h to qualify
	SplitCreditCapMin = 240 // credit is capped at 4h
)

// FDPEntry is one cell of the FDP limit table (R1): for a given report-time
// local band and segment-count bucket, the base FDP limit in hours.
type FDPEntry struct {
	Day         bool // true =昼间 [05:00,21:59), false = 夜间 [22:00,04:59)
	SegmentMin  int  // inclusive lower bound of segment bucket
	SegmentMax  int  // inclusive upper bound; 0 means unbounded
	LimitHours  int
}

// FDPLimitTable is the locked R1 table. Lookup order matters: the first
// matching entry (band + segment range) wins. The夜间 bucket covering 7+
// segments is the tightest at 10h.
var FDPLimitTable = []FDPEntry{
	// 昼间 [05:00, 21:59)
	{Day: true, SegmentMin: 1, SegmentMax: 2, LimitHours: 14},
	{Day: true, SegmentMin: 3, SegmentMax: 4, LimitHours: 13},
	{Day: true, SegmentMin: 5, SegmentMax: 6, LimitHours: 12},
	{Day: true, SegmentMin: 7, SegmentMax: 0, LimitHours: 11},
	// 夜间 [22:00, 04:59)
	{Day: false, SegmentMin: 1, SegmentMax: 2, LimitHours: 13},
	{Day: false, SegmentMin: 3, SegmentMax: 4, LimitHours: 12},
	{Day: false, SegmentMin: 5, SegmentMax: 6, LimitHours: 11},
	{Day: false, SegmentMin: 7, SegmentMax: 0, LimitHours: 10},
}

// FDPLimitHours returns the base FDP limit (R1, before augmented/split
// extensions) for a given report-time local hour and segment count.
//
//昼间 = local hour in [5, 21]; 夜间 = otherwise (22-23 and 0-4).
func FDPLimitHours(localHour int, segmentCount int) int {
	day := localHour >= 5 && localHour <= 21
	for _, e := range FDPLimitTable {
		if e.Day != day {
			continue
		}
		if segmentCount < e.SegmentMin {
			continue
		}
		if e.SegmentMax == 0 || segmentCount <= e.SegmentMax {
			return e.LimitHours
		}
	}
	// Defensive: segmentCount <= 0 should never reach here; default to the
	// tightest 夜间 bucket so a malformed input cannot inflate a limit.
	return 10
}

// SplitCreditMin returns the R3 split-duty FDP credit in minutes for a given
// in-duty break duration. Returns 0 if the break is below the 3h threshold.
// Credit is capped at 4h.
func SplitCreditMin(breakMin int) int {
	if breakMin < SplitBreakMinMin {
		return 0
	}
	if breakMin > SplitCreditCapMin {
		return SplitCreditCapMin
	}
	return breakMin
}

// Time-window helpers used across the fatigue and compliance packages.

// Window28d is the R4 rolling window.
const Window28d = 28 * 24 * time.Hour

// Window168h is the R5 rolling window.
const Window168h = 168 * time.Hour

// Window365d is the R6 rolling window.
const Window365d = 365 * 24 * time.Hour
