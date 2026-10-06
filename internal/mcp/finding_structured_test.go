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
