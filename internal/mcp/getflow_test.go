package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGetFlowUsesSliceAndPagesLargeBodies(t *testing.T) {
	var queries []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/flows/70/slice" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"head":"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n","mime":"text/plain","bodyBytes":5242880,"offset":0,"text":"0123","shown":4,"more":true,"nextOffset":4}`)
	}))
	defer mock.Close()
	out, err := New(mock.URL).Call("get_flow", map[string]any{"id": 70, "side": "response", "maxBytes": 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTP/1.1 200 OK", "Content-Type: text/plain", "0123", "offset=4", "5242880 bytes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("get_flow output missing %q:\n%s", want, out)
		}
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "side=res") || !strings.Contains(queries[0], "limit=4") {
		t.Fatalf("slice queries = %v", queries)
	}
}

func TestGetFlowForwardsGrepAndRendersMatches(t *testing.T) {
	var query string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"head":"HTTP/1.1 200 OK\r\n","bodyBytes":900,"grep":{"pattern":"token","count":1,"matches":[{"line":3,"offset":40,"text":"a\ntoken=x\nb"}]}}`)
	}))
	defer mock.Close()
	out, err := New(mock.URL).Call("get_flow", map[string]any{"id": 5, "side": "res", "grep": "token", "context": 1, "offset": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "grep=token") || !strings.Contains(query, "context=1") || !strings.Contains(query, "offset=10") {
		t.Fatalf("query = %s", query)
	}
	if !strings.Contains(out, "@ line 3 (byte 40)") || !strings.Contains(out, "token=x") {
		t.Fatalf("grep output:\n%s", out)
	}
}

func TestGetFlowDescribesBinaryBodies(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"head":"HTTP/1.1 200 OK\r\n","mime":"image/png","bodyBytes":204800,"bodyHash":"abcdef0123456789abcdef","binary":true,"hexHead":"00000000  89 50 4e 47  |.PNG|\n"}`)
	}))
	defer mock.Close()
	out, err := New(mock.URL).Call("get_flow", map[string]any{"id": 9, "side": "res"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[binary body: 204800 bytes, image/png") || !strings.Contains(out, "89 50 4e 47") {
		t.Fatalf("binary output:\n%s", out)
	}
}

// A nonexistent flow used to come back as 404 text inside a successful result.
func TestGetFlowMissingFlowIsToolError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"flow not found"}`)
	}))
	defer mock.Close()
	text, isErr := callResult(t, New(mock.URL), "get_flow", `{"id":424242}`)
	if !isErr || !strings.Contains(text, "flow not found") {
		t.Fatalf("missing flow = %q isError=%v", text, isErr)
	}
}

func TestTruncateKeepsRunesWhole(t *testing.T) {
	s := strings.Repeat("é", 10)
	out := truncate(s, 5)
	if !utf8.ValidString(out) {
		t.Fatalf("truncate split a rune: %q", out)
	}
	if got := firstLine(strings.Repeat("ü", 10), 3); !utf8.ValidString(got) || got != "üüü…" {
		t.Fatalf("firstLine = %q", got)
	}
}
