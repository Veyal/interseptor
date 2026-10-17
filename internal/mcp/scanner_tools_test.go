package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// run_scanner and list_issues returned the full 74 KB issue list for 2 real
// problems. They now ask the control API for a summary and grouped rows.
func TestScannerToolsRequestCompactViews(t *testing.T) {
	var got []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"total":0}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	if _, err := s.Call("run_scanner", map[string]any{"host": "example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("list_issues", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("list_issues", map[string]any{"view": "list", "severity": "high", "offset": 50}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /api/scanner/run?host=example.com&view=summary",
		"GET /api/scanner/issues?view=grouped",
		"GET /api/scanner/issues?offset=50&severity=high&view=list",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestScanReportIsBounded(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("# finding\n", 10000))
	}))
	defer mock.Close()
	out, err := New(mock.URL).Call("scan_report", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 20200 || !strings.Contains(out, "increase maxBytes") {
		t.Fatalf("scan_report = %d bytes", len(out))
	}
}
