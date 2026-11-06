package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// countingControl is a control API stub that records every hit and answers
// with reply.
func countingControl(t *testing.T, reply string) (*Server, *atomic.Int32, *map[string]any) {
	t.Helper()
	var hits atomic.Int32
	got := map[string]any{}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(mock.Close)
	return New(mock.URL), &hits, &got
}

func callResult(t *testing.T, s *Server, name, args string) (string, bool) {
	t.Helper()
	res := s.callTool([]byte(`{"name":"` + name + `","arguments":` + args + `}`))
	m, _ := res.(map[string]any)
	content, _ := m["content"].([]map[string]any)
	text := ""
	if len(content) > 0 {
		text, _ = content[0]["text"].(string)
	}
	return text, m["isError"] == true
}

// set_session {} used to reach the API and wipe the session headers even
// though enabled is required.
func TestValidationRejectsMissingRequiredBeforeAPI(t *testing.T) {
	s, hits, _ := countingControl(t, `{"ok":true}`)
	text, isErr := callResult(t, s, "set_session", `{}`)
	if !isErr || !strings.Contains(text, "enabled is required") {
		t.Fatalf("set_session {} = %q isError=%v", text, isErr)
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid call reached the control API %d times", hits.Load())
	}
}

func TestValidationNamesEnumValues(t *testing.T) {
	s, hits, _ := countingControl(t, `{}`)
	text, isErr := callResult(t, s, "decode", `{"op":"rot13","input":"x"}`)
	if !isErr || !strings.Contains(text, "op must be one of") || !strings.Contains(text, "base64decode") || !strings.Contains(text, `"rot13"`) {
		t.Fatalf("enum error = %q isError=%v", text, isErr)
	}
	// Case-insensitive match is accepted.
	if _, isErr := callResult(t, s, "decode", `{"op":"Base64Decode","input":"eA=="}`); isErr {
		t.Fatal("enum match must be case-insensitive")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestValidationRejectsWrongTypesAndBounds(t *testing.T) {
	s, hits, _ := countingControl(t, `{}`)
	cases := []struct{ tool, args, want string }{
		{"start_intruder", `{"target":"http://example.com","template":"GET / HTTP/1.1","threads":500}`, "threads must be <= 10"},
		{"start_intruder", `{"target":"http://example.com","template":"GET / HTTP/1.1","threads":1.5}`, "threads must be an integer"},
		{"send_request", `{"url":{"u":"http://example.com"}}`, "url must be a string (got object"},
		{"list_flows", `{"status":9}`, "status must be <= 5"},
		{"list_flows", `{"includeTools":"maybe"}`, "includeTools must be a boolean"},
	}
	for _, c := range cases {
		text, isErr := callResult(t, s, c.tool, c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s %s = %q isError=%v, want %q", c.tool, c.args, text, isErr, c.want)
		}
	}
	_ = hits
}

func TestValidationNormalizesBooleanStrings(t *testing.T) {
	var gotQuery string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"flows":[]}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	if _, isErr := callResult(t, s, "list_flows", `{"includeTools":"false"}`); isErr {
		t.Fatal("\"false\" should be accepted for a boolean")
	}
	if strings.Contains(gotQuery, "includeTools=1") {
		t.Fatalf("includeTools:\"false\" was ignored: %s", gotQuery)
	}
}

func TestStartIntruderDefaultsAttackType(t *testing.T) {
	s, _, got := countingControl(t, `{"running":true}`)
	if _, isErr := callResult(t, s, "start_intruder", `{"target":"http://example.com","template":"GET /§a§ HTTP/1.1","payloads":[["x"]]}`); isErr {
		t.Fatal("start_intruder without attackType should default")
	}
	if (*got)["attackType"] != "sniper" {
		t.Fatalf("attackType = %v, want sniper", (*got)["attackType"])
	}
	if _, isErr := callResult(t, s, "start_intruder", `{"target":"http://example.com","template":"GET / HTTP/1.1","repeat":3}`); isErr {
		t.Fatal("repeat without attackType should default")
	}
	if (*got)["attackType"] != "repeat" {
		t.Fatalf("attackType = %v, want repeat", (*got)["attackType"])
	}
}

// /api/decode answers 200 {"error":...} for the UI; the agent must see isError.
func TestErrorOnlyReplyIsToolError(t *testing.T) {
	s, _, _ := countingControl(t, `{"error":"illegal base64 data at input byte 0"}`)
	text, isErr := callResult(t, s, "decode", `{"op":"base64decode","input":"%%%"}`)
	if !isErr || !strings.Contains(text, "illegal base64") {
		t.Fatalf("error-only reply = %q isError=%v", text, isErr)
	}
}
