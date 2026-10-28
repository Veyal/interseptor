package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// findingStatusMock answers create/update with a finding the server coerced to
// needs_verification because the impact was not demonstrated.
func findingStatusMock(t *testing.T, flowAttach int) *httptest.Server {
	t.Helper()
	const coerced = `{"id":4,"title":"t","status":"needs_verification","proofReview":{"execution":"prerequisite_only","reason":"rooted device"},"blocks":[]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings":
			io.WriteString(w, coerced)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/findings/4":
			io.WriteString(w, coerced)
		case r.Method == http.MethodGet && r.URL.Path == "/api/flows/7":
			io.WriteString(w, `{"id":7,"scheme":"https","host":"example.com","path":"/a","method":"GET","status":200}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings/4/flows":
			w.WriteHeader(flowAttach)
			io.WriteString(w, `{"error":"flow 7 not found"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCreateAndUpdateFindingReportStatusCoercion(t *testing.T) {
	mock := findingStatusMock(t, http.StatusOK)
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	want := "status coerced to needs_verification: impact not demonstrated (proofReview.execution=prerequisite_only)"

	out, err := s.Call("create_finding", map[string]any{"title": "t", "severity": "High", "status": "verified"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("create_finding should explain the coerced status:\n%s", out)
	}
	out, err = s.Call("update_finding", map[string]any{"id": 4, "status": "open"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("update_finding should explain the coerced status:\n%s", out)
	}
	// Asking for what the server kept is not a coercion.
	out, err = s.Call("update_finding", map[string]any{"id": 4, "status": "triage"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "coerced") {
		t.Fatalf("an alias of the kept status is not a coercion:\n%s", out)
	}
}
