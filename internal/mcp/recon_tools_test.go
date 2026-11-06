package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListFlowsPagesAndFixesSearchScope(t *testing.T) {
	var query string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"flows":[],"truncated":false}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	if _, err := s.Call("list_flows", map[string]any{"cursor": 120, "search": "hello", "searchScope": "body"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"before=120", "search=hello", "searchScope=body"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q missing %q", query, want)
		}
	}
	// Default description no longer claims "path substring"; the schema offers searchScope.
	_, schema, _ := s.ToolMeta("list_flows")
	props := schema["properties"].(map[string]any)
	if _, ok := props["searchScope"]; !ok {
		t.Fatal("list_flows should expose searchScope")
	}
}

func TestListEndpointsAndParams(t *testing.T) {
	var paths []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	if _, err := s.Call("list_endpoints", map[string]any{"host": "example.com", "showNoise": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("list_params", map[string]any{"host": "example.com", "inScope": true, "limit": 100}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.HasPrefix(paths[0], "/api/endpoints?") || !strings.Contains(paths[0], "hideNoise=0") {
		t.Fatalf("endpoints path = %v", paths)
	}
	if !strings.HasPrefix(paths[1], "/api/params?") || !strings.Contains(paths[1], "inScope=1") || !strings.Contains(paths[1], "limit=100") {
		t.Fatalf("params path = %v", paths)
	}
}

func TestIntruderStatePagesAndFilters(t *testing.T) {
	// 300 results, every 50th an anomaly past the first page.
	var results []string
	for i := 0; i < 300; i++ {
		anom := i%50 == 0 && i > 0
		results = append(results, fmt.Sprintf(`{"id":%d,"status":200,"anomaly":%t}`, i, anom))
	}
	var resultsQuery string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/intruder/state":
			_, _ = io.WriteString(w, `{"running":false,"total":300,"done":300,"runId":"abc","attack":"sniper","results":[{"id":0}]}`)
		case "/api/intruder/results":
			resultsQuery = r.URL.RawQuery
			off := atoiDefault(r.URL.Query().Get("offset"), 0)
			lim := atoiDefault(r.URL.Query().Get("limit"), 200)
			end := off + lim
			if end > len(results) {
				end = len(results)
			}
			page := results[off:end]
			_, _ = io.WriteString(w, fmt.Sprintf(`{"results":[%s],"total":300,"offset":%d,"limit":%d}`, strings.Join(page, ","), off, lim))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	s := New(mock.URL)

	out, err := s.Call("intruder_state", map[string]any{"limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Results    []json.RawMessage `json:"results"`
		Total      int               `json:"total"`
		NextOffset int               `json:"nextOffset"`
		RunID      string            `json:"runId"`
		Attack     string            `json:"attack"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if st.Total != 300 || len(st.Results) != 100 || st.NextOffset != 100 || st.RunID != "abc" || st.Attack != "sniper" {
		t.Fatalf("page 1 = total %d len %d next %d run %q", st.Total, len(st.Results), st.NextOffset, st.RunID)
	}

	out, _ = s.Call("intruder_state", map[string]any{"onlyAnomalies": true, "limit": 10})
	_ = json.Unmarshal([]byte(out), &st)
	if len(st.Results) != 5 { // ids 50,100,150,200,250 — all past the first page
		t.Fatalf("anomalies = %d (%s)", len(st.Results), out)
	}
	if !strings.Contains(resultsQuery, "limit=5000") {
		t.Fatalf("anomaly scan should widen the window: %s", resultsQuery)
	}
}

func atoiDefault(s string, d int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return d
	}
	return n
}
