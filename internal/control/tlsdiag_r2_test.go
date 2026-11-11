package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func r2HTTPSFlows(t *testing.T, st *store.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := st.InsertFlow(&store.Flow{
			TS: time.Now().Add(-time.Minute), Method: "GET", Scheme: "https", Host: "api.example.com", Port: 443,
			Path: "/me", Status: 200,
		}); err != nil {
			t.Fatalf("insert success: %v", err)
		}
	}
}

// The diagnosis needs to know that interception works and when it last did,
// not how many flows prove it. Past the cap the count stops being read (so the
// endpoint does not get slower as History grows) and the report says so
// instead of printing a number that looks exact.
func TestR2TLSDiagnosisCapsTheSuccessCount(t *testing.T) {
	defer func(old int64) { tlsHTTPSOkCountCap = old }(tlsHTTPSOkCountCap)
	tlsHTTPSOkCountCap = 3
	h, st, _ := newHub(t)
	r2HTTPSFlows(t, st, 5)

	rep := (&authzAPI{h}).buildTLSDiagnosis("")
	if rep.Verdict != "ok" {
		t.Fatalf("verdict = %q (%s), want ok", rep.Verdict, rep.Detail)
	}
	if rep.HTTPSOkCount != 3 || !rep.HTTPSOkCountCapped {
		t.Fatalf("httpsOkCount = %d capped = %v, want 3 and capped", rep.HTTPSOkCount, rep.HTTPSOkCountCapped)
	}
	if !strings.Contains(rep.Detail, "3+ successful HTTPS flows") {
		t.Fatalf("detail = %q, want the capped count written as 3+", rep.Detail)
	}
	if rep.TotalFlows != 5 {
		t.Fatalf("totalFlows = %d, want 5", rep.TotalFlows)
	}
}

// Under the cap the count is exact and the wording is unchanged.
func TestR2TLSDiagnosisKeepsAnExactCountUnderTheCap(t *testing.T) {
	h, st, _ := newHub(t)
	r2HTTPSFlows(t, st, 2)
	rep := (&authzAPI{h}).buildTLSDiagnosis("")
	if rep.HTTPSOkCount != 2 || rep.HTTPSOkCountCapped {
		t.Fatalf("httpsOkCount = %d capped = %v, want 2 and not capped", rep.HTTPSOkCount, rep.HTTPSOkCountCapped)
	}
	if want := "HTTPS interception is working (2 successful HTTPS flows, 2 total)"; rep.Detail != want {
		t.Fatalf("detail = %q, want %q", rep.Detail, want)
	}
	if got := humanCount(10000); got != "10,000" {
		t.Fatalf("humanCount(10000) = %q", got)
	}
	if got := humanCount(999); got != "999" {
		t.Fatalf("humanCount(999) = %q", got)
	}
	if got := humanCount(1234567); got != "1,234,567" {
		t.Fatalf("humanCount(1234567) = %q", got)
	}
}

// The UI and the MCP tools read these fields by name; httpsOkCount stays a
// number and the readiness row keeps its id, ok flag and detail.
func TestR2TLSEndpointsKeepTheirJSONShape(t *testing.T) {
	defer func(old int64) { tlsHTTPSOkCountCap = old }(tlsHTTPSOkCountCap)
	tlsHTTPSOkCountCap = 2
	h, st, _ := newHub(t)
	r2HTTPSFlows(t, st, 4)

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func(path string) map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}
	diag := get("/api/tls-diagnosis")
	for _, k := range []string{"tlsFailureCount", "recentFailureCount", "httpsOkCount", "totalFlows"} {
		if _, ok := diag[k].(float64); !ok {
			t.Errorf("tls-diagnosis %s = %#v, want a number", k, diag[k])
		}
	}
	if diag["verdict"] != "ok" || diag["canBypass"] != false || diag["httpsOkCountCapped"] != true {
		t.Errorf("tls-diagnosis verdict/canBypass/httpsOkCountCapped = %v/%v/%v", diag["verdict"], diag["canBypass"], diag["httpsOkCountCapped"])
	}
	if diag["httpsOkCount"].(float64) != 2 || diag["totalFlows"].(float64) != 4 {
		t.Errorf("httpsOkCount/totalFlows = %v/%v, want 2/4", diag["httpsOkCount"], diag["totalFlows"])
	}

	ready := get("/api/readiness")
	checks, _ := ready["checks"].([]any)
	var row map[string]any
	for _, c := range checks {
		if m, _ := c.(map[string]any); m["id"] == "tls_intercept" {
			row = m
		}
	}
	if row == nil || row["ok"] != true || row["detail"] != diag["detail"] {
		t.Fatalf("tls_intercept row = %#v, want ok with the diagnosis detail %q", row, diag["detail"])
	}
}
