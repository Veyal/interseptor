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

func TestMCPFindingStructuredFieldsSchemaAndForwarding(t *testing.T) {
	caps := New("http://127.0.0.1:1").Capabilities()["finding"].(map[string]any)
	for _, op := range []string{"createFields", "updateFields"} {
		fields := caps[op].([]string)
		for _, want := range []string{"claims", "notExecuted", "relatedFindings"} {
			found := false
			for _, f := range fields {
				found = found || f == want
			}
			if !found {
				t.Fatalf("%s missing %q: %v", op, want, fields)
			}
		}
	}
	var createBody, updateBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings":
			json.NewDecoder(r.Body).Decode(&createBody)
			io.WriteString(w, `{"id":1,"title":"t","flows":[],"blocks":[]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/findings/1":
			json.NewDecoder(r.Body).Decode(&updateBody)
			io.WriteString(w, `{"id":1,"title":"t","flows":[],"blocks":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	extra := `"claims":[{"id":"c1","statement":"s","verdict":"refuted"}],"notExecuted":[{"method":"POST","target":"https://example.com/a","reason":"destructive"}],"relatedFindings":[{"id":2,"relation":"chain"}]`
	script := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"t",` + extra + `}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_finding","arguments":{"id":1,` + extra + `}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]map[string]any{"create": createBody, "update": updateBody} {
		for _, k := range []string{"claims", "notExecuted", "relatedFindings"} {
			if _, ok := body[k]; !ok {
				t.Fatalf("%s did not forward %q: %v", name, k, body)
			}
		}
	}
}

func mcpToolText(t *testing.T, s *Server, call string) (string, bool) {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(call+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("bad response %q: %v", out.String(), err)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func TestMCPHardRejectionsAlwaysStartWithErrorMarker(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid finding: proofReview.evidence.result is empty; send one flowId or a 64-character hash"}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	for name, call := range map[string]string{
		"store evidence mapping": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"t","proofReview":{"evidence":{"result":{"hash":""}}}}}}`,
		"blocks not array":       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"t","blocks":"nope"}}}`,
		"blocks and body":        `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"t","blocks":[],"body":"x"}}}`,
		"missing id":             `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_finding","arguments":{}}}`,
		"unknown tool":           `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`,
	} {
		text, isErr := mcpToolText(t, s, call)
		if !isErr || !strings.HasPrefix(text, "error:") {
			t.Errorf("%s: isError=%v text=%q, want error: prefix", name, isErr, text)
		}
	}
	text, _ := mcpToolText(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"t","proofReview":{"evidence":{"result":{"hash":""}}}}}}`)
	if !strings.Contains(text, "proofReview.evidence.result") {
		t.Errorf("store message lost: %q", text)
	}
}
