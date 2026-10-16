package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A WebSocket flow recorded by ws_send is cited like any other flow block: the
// finding format validator accepts it with no evidence warnings, and the guide
// tells agents how.
func TestWSFlowEvidenceAcceptedByFindingFormat(t *testing.T) {
	body := `[{"type":"text","role":"action","md":"Replay the upgrade with an invalid token."},{"type":"flow","role":"result","flowId":42,"note":"WS handshake and frames","proof":"Server answers Invalid token then closes."},{"type":"flow","role":"control","flowId":41,"proof":"Valid token is accepted."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Medium", Summary: "Session token accepted in WebSocket URL query.",
		Impact: "token leakage via logs", Why: "secrets belong in headers", Target: "wss://api.example.com/stream",
		Fix: "authenticate after connect", Retest: "query tokens are rejected", Body: body,
	})
	if err != nil {
		t.Fatalf("validator rejected WS flow evidence: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "missing evidence") || strings.Contains(w, "no captured flow") {
			t.Fatalf("unexpected evidence warning for WS flow blocks: %q", w)
		}
	}
	if !strings.Contains(findingFormatGuide, "ws_send") {
		t.Fatal("finding format guide should explain citing ws_send flows")
	}
}

func TestSetWSFrameNoteTool(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("set_ws_frame_note", map[string]any{"id": 9, "frameId": 3, "note": "token rejected"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/flows/9/ws/3/note" || gotBody["note"] != "token rejected" {
		t.Fatalf("got %s %s %v", gotMethod, gotPath, gotBody)
	}
	if _, err := s.Call("set_ws_frame_note", map[string]any{"id": 9}); err == nil {
		t.Fatal("frameId must be required")
	}
}
