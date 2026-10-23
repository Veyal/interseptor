package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunToolRecoversFromPanic(t *testing.T) {
	s := New("http://127.0.0.1:1")
	s.report = func(Activity) {}
	s.add("boom_tool", "panics", obj(map[string]any{}), func(map[string]any) (string, error) { panic("kaboom") })
	_, err := s.Call("boom_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v, want a recovered panic error", err)
	}
}

func TestServeRejectsOversizedLineAndContinues(t *testing.T) {
	old := maxStdioLineBytes
	maxStdioLineBytes = 64
	defer func() { maxStdioLineBytes = old }()

	s := New("http://127.0.0.1:1")
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", 4096) + `"}}`
	in := strings.NewReader(big + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %q, want an error then the ping result", out.String())
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if first["error"] == nil {
		t.Fatalf("oversized line was not rejected: %s", lines[0])
	}
	if second["id"] != float64(2) || second["error"] != nil {
		t.Fatalf("server did not continue after the oversized line: %s", lines[1])
	}
}

func TestGetFlowEscapesSide(t *testing.T) {
	var gotQuery string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		io.WriteString(w, "raw")
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	_, _ = s.Call("get_flow", map[string]any{"id": 1, "side": "req&evil=1"})
	if strings.Contains(gotQuery, "&evil=1") || !strings.Contains(gotQuery, "side=req%26evil%3D1") {
		t.Fatalf("side was not query-escaped: %q", gotQuery)
	}
}
