package fatigue

import (
	"testing"
	"time"

	"task124-crewfatigue/internal/domain"
)

func mustT(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestFDPLimitTableDay(t *testing.T) {
	cases := []struct{ hour, segs, want int }{
		{12, 1, 14}, //昼间 1-2 seg → 14
		{12, 2, 14},
		{12, 3, 13}, // 3-4 → 13
		{12, 4, 13},
		{12, 5, 12}, // 5-6 → 12
		{12, 6, 12},
		{12, 7, 11}, // 7+ → 11
		{12, 9, 11},
	}
	for _, c := range cases {
		if got := domain.FDPLimitHours(c.hour, c.segs); got != c.want {
			t.Errorf("FDPLimitHours(day,%d segs)=%d want %d", c.segs, got, c.want)
		}
	}
}

func TestFDPLimitTableNight(t *testing.T) {
	cases := []struct{ hour, segs, want int }{
		{22, 1, 13}, //夜间 1-2 → 13
		{0, 2, 13},
		{3, 3, 12}, // 3-4 → 12
		{23, 5, 11}, // 5-6 → 11
		{4, 7, 10}, // 7+ → 10
		{1, 12, 10},
	}
	for _, c := range cases {
		if got := domain.FDPLimitHours(c.hour, c.segs); got != c.want {
			t.Errorf("FDPLimitHours(night hour=%d,%d segs)=%d want %d", c.hour, c.segs, got, c.want)
		}
	}
}

func TestSplitCredit(t *testing.T) {
	cases := []struct{ breakMin, want int }{
		{0, 0},   // no break → no credit
		{179, 0}, // below 3h threshold → no credit
		{180, 180}, // exactly 3h → full
		{240, 240}, // 4h → full (the cap)
		{300, 240}, // 5h → capped at 4h
		{600, 240}, // 10h → capped at 4h
	}
	for _, c := range cases {
		if got := domain.SplitCreditMin(c.breakMin); got != c.want {
			t.Errorf("SplitCreditMin(%d)=%d want %d", c.breakMin, got, c.want)
		}
	}
}

func TestFacilityExtensionHours(t *testing.T) {
	if domain.FacilityNone.ExtensionHours() != 0 {
		t.Errorf("NONE extension want 0")
	}
	if domain.FacilityClass3.ExtensionHours() != 2 {
		t.Errorf("CLASS_3 extension want 2")
	}
	if domain.FacilityClass2.ExtensionHours() != 3 {
		t.Errorf("CLASS_2 extension want 3")
	}
	if domain.FacilityClass1.ExtensionHours() != 4 {
		t.Errorf("CLASS_1 extension want 4")
	}
}

func TestEvalLegalSimple(t *testing.T) {
	// 2-segment昼间 trip, FDP 225min < 14h(840). No history → no R4/R5/R6/R7/R8/R9.
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	in := EvalInput{
		T:    mustT("2026-08-18T12:00:00Z"),
		Crew: crew,
		Proposed: ProposedDuty{
			ReportTime:    mustT("2026-08-18T04:00:00Z"),
			ReleaseTime:   mustT("2026-08-18T07:45:00Z"),
			SegmentCount:  2,
			AircraftFacility: domain.FacilityNone,
		},
	}
	r := Eval(in)
	if r.Verdict != domain.VerdictLegal {
		t.Fatalf("want LEGAL got %s %+v", r.Verdict, r.Violations)
	}
	if r.Metrics.FDPMin != 225 {
		t.Errorf("FDP=%d want 225", r.Metrics.FDPMin)
	}
	if r.Metrics.FDPLimitMin != 840 {
		t.Errorf("limit=%d want 840", r.Metrics.FDPLimitMin)
	}
}

func TestEvalFDPExceed(t *testing.T) {
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	// 7 seg by day → 11h=660. FDP 700 → exceeds.
	in := EvalInput{
		T:    mustT("2026-08-18T12:00:00Z"),
		Crew: crew,
		Proposed: ProposedDuty{
			ReportTime:    mustT("2026-08-18T04:00:00Z"),
			ReleaseTime:   mustT("2026-08-18T15:40:00Z"), // 700min
			SegmentCount:  7,
			AircraftFacility: domain.FacilityNone,
		},
	}
	r := Eval(in)
	if r.Verdict != domain.VerdictIllegal {
		t.Fatalf("want ILLEGAL got %s", r.Verdict)
	}
	if !hasRuleCode(r.Violations, domain.RuleFDP) {
		t.Errorf("missing R1 violation")
	}
}

func TestEvalAugmentedExtension(t *testing.T) {
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	// Same FDP 700, but augmented CLASS_1 → limit 660+240=900, so legal.
	in := EvalInput{
		T:    mustT("2026-08-18T12:00:00Z"),
		Crew: crew,
		Proposed: ProposedDuty{
			ReportTime:    mustT("2026-08-18T04:00:00Z"),
			ReleaseTime:   mustT("2026-08-18T15:40:00Z"),
			SegmentCount:  7,
			IsAugmented:   true,
			AircraftFacility: domain.FacilityClass1,
		},
	}
	r := Eval(in)
	if r.Metrics.FDPLimitMin != 900 {
		t.Errorf("augmented limit=%d want 900", r.Metrics.FDPLimitMin)
	}
	if r.Verdict != domain.VerdictLegal {
		t.Errorf("augmented want LEGAL got %s %+v", r.Verdict, r.Violations)
	}
}

func TestEvalSplitDutyCredit(t *testing.T) {
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	// 3 seg day → 13h=780. FDP 800 (>780). With 240min split → 780+240=1020 → legal.
	in := EvalInput{
		T:    mustT("2026-08-18T12:00:00Z"),
		Crew: crew,
		Proposed: ProposedDuty{
			ReportTime:    mustT("2026-08-18T04:00:00Z"),
			ReleaseTime:   mustT("2026-08-18T17:20:00Z"), // 800min
			SegmentCount:  3,
			SplitBreakMin: 240,
			AircraftFacility: domain.FacilityNone,
		},
	}
	r := Eval(in)
	if r.Metrics.FDPLimitMin != 1020 {
		t.Errorf("split limit=%d want 1020", r.Metrics.FDPLimitMin)
	}
	if r.Verdict != domain.VerdictLegal {
		t.Errorf("split want LEGAL got %s %+v", r.Verdict, r.Violations)
	}
}

func TestEvalCumulative28d(t *testing.T) {
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	// 28d limit = 100h = 6000min. Pre-load 6050min.
	segs := []domain.FlightSegment{{BlockTimeMin: 6050}}
	in := EvalInput{
		T: mustT("2026-08-18T12:00:00Z"), Crew: crew,
		Proposed: ProposedDuty{
			ReportTime: mustT("2026-08-18T04:00:00Z"), ReleaseTime: mustT("2026-08-18T05:00:00Z"),
			SegmentCount: 1, AircraftFacility: domain.FacilityNone,
		},
		LandedIn28d: segs,
	}
	r := Eval(in)
	if !hasRuleCode(r.Violations, domain.RuleCum28d) {
		t.Errorf("missing R4 violation; got %+v", r.Violations)
	}
}

func TestEvalEarlyStartStreak(t *testing.T) {
	crew := &domain.CrewMember{ID: 1, HomeTZ: "Asia/Shanghai"}
	// Report 21:00Z Aug17 = local 05:00 Aug18 (Asia/Shanghai +8) → early (<07:00).
	// Streak carried = 5, this is the 6th → R9.
	in := EvalInput{
		T: mustT("2026-08-18T12:00:00Z"), Crew: crew,
		Proposed: ProposedDuty{
			ReportTime: mustT("2026-08-17T21:00:00Z"), ReleaseTime: mustT("2026-08-17T22:00:00Z"),
			SegmentCount: 1, AircraftFacility: domain.FacilityNone,
		},
		EarlyStartStreak: 5,
	}
	r := Eval(in)
	if r.Metrics.EarlyStartStreak != 6 {
		t.Errorf("streak=%d want 6", r.Metrics.EarlyStartStreak)
	}
	if !hasRuleCode(r.Violations, domain.RuleEarlyStart) {
		t.Errorf("missing R9 violation; got %+v", r.Violations)
	}
}

func hasRuleCode(vs []domain.Violation, code domain.RuleCode) bool {
	for _, v := range vs {
		if v.Rule == code {
			return true
		}
	}
	return false
}
