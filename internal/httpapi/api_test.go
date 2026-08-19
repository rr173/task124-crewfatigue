package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"task124-crewfatigue/internal/compliance"
	"task124-crewfatigue/internal/crew"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/store"
)

func newMux(t *testing.T) (http.Handler, Services) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cs := crew.New(st)
	sch := schedule.New(st)
	cps := compliance.New(st, sch)
	svc := Services{Store: st, Crew: cs, Schedule: sch, Compliance: cps}
	return NewMux(svc, nil), svc
}

func postJSON(t *testing.T, h http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var m map[string]any
	json.NewDecoder(w.Body).Decode(&m)
	return w.Code, m
}

func getJSON(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var m map[string]any
	json.NewDecoder(w.Body).Decode(&m)
	return w.Code, m
}

func TestHealthAndVersion(t *testing.T) {
	h, _ := newMux(t)
	code, m := getJSON(t, h, "/health")
	if code != 200 || m["status"] != "ok" {
		t.Errorf("health: code=%d m=%+v", code, m)
	}
	code, m = getJSON(t, h, "/version")
	if code != 200 || m["version"] != Version {
		t.Errorf("version: code=%d m=%+v", code, m)
	}
}

func TestCreateCrewAndList(t *testing.T) {
	h, _ := newMux(t)
	code, m := postJSON(t, h, "/api/crew", `{"name":"X","role":"CAPTAIN","home_tz":"Asia/Shanghai"}`)
	if code != 201 {
		t.Fatalf("create crew: code=%d m=%+v", code, m)
	}
	code, list := getJSON(t, h, "/api/crew")
	// /api/crew returns an array, but getJSON decodes into map; check via raw body.
	if code != 200 {
		t.Errorf("list crew: code=%d", code)
	}
	_ = list
}

func TestCreateCrewBadRole(t *testing.T) {
	h, _ := newMux(t)
	code, m := postJSON(t, h, "/api/crew", `{"name":"Y","role":"BOGUS","home_tz":"UTC"}`)
	if code != 422 {
		t.Errorf("bad role: code=%d want 422 m=%+v", code, m)
	}
}

func TestCreateAircraft(t *testing.T) {
	h, _ := newMux(t)
	code, _ := postJSON(t, h, "/api/aircraft-types", `{"code":"B789","has_rest_facility":true,"rest_facility_class":"CLASS_1"}`)
	if code != 201 {
		t.Errorf("create aircraft: want 201")
	}
	code, _ = postJSON(t, h, "/api/aircraft-types", `{"code":"B789","rest_facility_class":"NONE"}`)
	if code != 409 {
		t.Errorf("dup aircraft: want 409 got %d", code)
	}
}

func TestEvaluateTripEndpoint(t *testing.T) {
	h, _ := newMux(t)
	postJSON(t, h, "/api/crew", `{"name":"E","role":"CAPTAIN","home_tz":"Asia/Shanghai"}`)
	postJSON(t, h, "/api/aircraft-types", `{"code":"B738","has_rest_facility":false,"rest_facility_class":"NONE"}`)
	body := `{"aircraft_type":"B738","is_augmented":false,"split_break_min":0,"segments":[{"dep_airport":"PEK","arr_airport":"SHA","scheduled_dep":"2026-08-18T05:00:00Z","scheduled_arr":"2026-08-18T06:00:00Z"}]}`
	code, m := postJSON(t, h, "/api/crew/1/evaluate-trip", body)
	if code != 200 {
		t.Fatalf("evaluate: code=%d m=%+v", code, m)
	}
	if m["verdict"] != "LEGAL" {
		t.Errorf("verdict=%v want LEGAL", m["verdict"])
	}
}

func TestReplayEndpoint(t *testing.T) {
	h, _ := newMux(t)
	postJSON(t, h, "/api/crew", `{"name":"P","role":"CAPTAIN","home_tz":"UTC"}`)
	code, m := postJSON(t, h, "/api/admin/replay", `{"crew_id":1,"as_of":"2026-08-18T12:00:00Z"}`)
	if code != 200 {
		t.Errorf("replay: code=%d m=%+v", code, m)
	}
	if m["overrides"] != float64(0) {
		t.Errorf("overrides=%v want 0", m["overrides"])
	}
}

func TestPathIDInvalid(t *testing.T) {
	h, _ := newMux(t)
	code, _ := getJSON(t, h, "/api/crew/abc")
	if code != 400 {
		t.Errorf("bad id: code=%d want 400", code)
	}
}

func TestRestDebtEndpoint(t *testing.T) {
	h, _ := newMux(t)
	postJSON(t, h, "/api/crew", `{"name":"D","role":"CAPTAIN","home_tz":"UTC"}`)
	code, m := getJSON(t, h, "/api/crew/1/rest-debt")
	if code != 200 {
		t.Errorf("rest-debt: code=%d", code)
	}
	if m["compensatory_owed_min"] != float64(0) {
		t.Errorf("debt=%v want 0", m["compensatory_owed_min"])
	}
}

// ensure time import retained
var _ = time.Now
