package intruder

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A run can carry more than one match rule and more than one extract rule; each
// result reports a per-rule hit vector and a per-rule capture.
func TestMultipleMatchAndExtractRules(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"role":"admin","token":"abc123","ok":true}`)
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	if err := e.Start(Spec{
		Target:       upstream.URL,
		Template:     "GET /x?u=§seed§ HTTP/1.1\nHost: h\n\n",
		AttackType:   "sniper",
		Payloads:     [][]string{{"1"}},
		MatchRules:   []string{`"ok":true`, `nope-not-present`},
		ExtractRules: []string{`"token":"([^"]+)"`, `"role":"([^"]+)"`},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	if len(res.Matches) != 2 || !res.Matches[0] || res.Matches[1] {
		t.Fatalf("Matches = %+v, want [true false]", res.Matches)
	}
	if !res.Matched {
		t.Fatalf("Matched (any) should be true: %+v", res)
	}
	if len(res.Extracts) != 2 || res.Extracts[0] != "abc123" || res.Extracts[1] != "admin" {
		t.Fatalf("Extracts = %+v, want [abc123 admin]", res.Extracts)
	}
	if res.Extracted != "abc123" {
		t.Fatalf("Extracted (first) = %q, want abc123", res.Extracted)
	}
}

// The legacy single GrepMatch/GrepExtract still work and feed the same rule set.
func TestLegacySingleGrepStillWorks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "value=42")
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	if err := e.Start(Spec{
		Target:      upstream.URL,
		Template:    "GET /x?u=§seed§ HTTP/1.1\nHost: h\n\n",
		AttackType:  "sniper",
		Payloads:    [][]string{{"1"}},
		GrepMatch:   "value",
		GrepExtract: `value=(\d+)`,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	res := st.Results[0]
	if !res.Matched || res.Extracted != "42" {
		t.Fatalf("legacy grep broke: matched=%v extracted=%q", res.Matched, res.Extracted)
	}
}
