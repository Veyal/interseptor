package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// run_scanner and list_issues used to return every issue in full — 74 KB each
// for 131 issues that were only 2 (check, host) pairs. run_scanner now asks for
// counts and list_issues for grouped, bounded rows by default.
func TestScannerToolsRequestCountsAndGroupedIssues(t *testing.T) {
	var got []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"total":0}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("run_scanner", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("list_issues", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("list_issues", map[string]any{"grouped": false, "severity": "High", "host": "app.example.com", "title": "cors", "limit": float64(5), "offset": float64(10)}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "POST /api/scanner/run?summary=1" {
		t.Fatalf("calls = %v", got)
	}
	q := query(t, got[1])
	if q.Get("group") != "host" || q.Get("limit") != "50" {
		t.Fatalf("default list_issues query = %v, want group=host limit=50", q)
	}
	q = query(t, got[2])
	if q.Has("group") || q.Get("limit") != "5" || q.Get("offset") != "10" || q.Get("severity") != "High" || q.Get("host") != "app.example.com" || q.Get("title") != "cors" {
		t.Fatalf("raw list_issues query = %v", q)
	}
}

func query(t *testing.T, call string) url.Values {
	t.Helper()
	u, err := url.Parse(call[len("GET "):])
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}
