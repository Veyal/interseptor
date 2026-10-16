package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPUpdateFindingSurfacesEnvironmentValidationError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid finding: environment must be production, staging, development, testing, local, or legacy prod"}`))
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_finding","arguments":{"id":42,"environment":"qa-lab"}}}` + "\n"
	var out strings.Builder
	if err := s.Serve(strings.NewReader(call), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "isError") || !strings.Contains(out.String(), "environment must be") {
		t.Fatalf("validation error not surfaced: %s", out.String())
	}
}
