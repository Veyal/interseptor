package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestRecordFindingFromFlowReportsAttachFailure(t *testing.T) {
	mock := findingStatusMock(t, http.StatusNotFound)
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	out, err := s.Call("record_finding_from_flow", map[string]any{"flowId": 7, "title": "t"})
	if err == nil {
		t.Fatalf("a failed flow attach must be an error, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "#4") || !strings.Contains(err.Error(), "flow 7") {
		t.Fatalf("error should name the created finding and the flow: %v", err)
	}
	if strings.Contains(out, "attached as") {
		t.Fatalf("must not claim the flow was attached:\n%s", out)
	}
}
