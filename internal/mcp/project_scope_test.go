package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// projectStub is a control plane that names one project and counts calls.
func projectStub(t *testing.T, name, dir string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-Interseptor-Project", name)
		if dir != "" {
			w.Header().Set("X-Interseptor-Project-Dir", dir)
		}
		switch r.URL.Path {
		case "/api/project":
			_ = json.NewEncoder(w).Encode(map[string]any{"current": name, "dir": dir})
		case "/api/decode":
			_ = json.NewEncoder(w).Encode(map[string]any{"output": "x"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 0})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Every result must name the project it acted on: an agent that reads only the
// response must never have to guess which engagement it just changed.
func TestToolResultsNameTheProject(t *testing.T) {
	srv, calls := projectStub(t, "acme-prod", "/tmp/projects/acme-prod")
	s := New(srv.URL)

	text, err := s.Call("decode", map[string]any{"op": "base64encode", "input": "aGk="})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(text, "project: acme-prod (/tmp/projects/acme-prod)") {
		t.Fatalf("result does not name the project: %q", text)
	}
	// The label rides on the response the tool already made.
	if *calls != 1 {
		t.Fatalf("project labelling cost an extra API call: %d", *calls)
	}
}

func TestDestructiveToolRejectsAWrongExpectedProject(t *testing.T) {
	srv, _ := projectStub(t, "acme-prod", "/tmp/projects/acme-prod")
	s := New(srv.URL)

	_, err := s.Call("prune_history", map[string]any{
		"hosts": "noise.example", "mode": "delete", "expectedProject": "acme-staging",
	})
	if err == nil {
		t.Fatal("a prune pinned to another project must be rejected")
	}
	if !strings.Contains(err.Error(), "acme-prod") {
		t.Fatalf("rejection must name the active project: %v", err)
	}
}

func TestDestructiveToolAcceptsTheActiveProject(t *testing.T) {
	srv, _ := projectStub(t, "acme-prod", "/tmp/projects/acme-prod")
	s := New(srv.URL)

	if _, err := s.Call("prune_history", map[string]any{
		"hosts": "noise.example", "mode": "delete", "expectedProject": "acme-prod", "dryRun": true,
	}); err != nil {
		t.Fatalf("matching pin rejected: %v", err)
	}
}

// A non-destructive tool ignores the pin rather than failing on it.
func TestNonDestructiveToolIgnoresExpectedProject(t *testing.T) {
	srv, _ := projectStub(t, "acme-prod", "")
	s := New(srv.URL)
	if _, err := s.Call("decode", map[string]any{"op": "base64encode", "input": "aGk=", "expectedProject": "other"}); err != nil {
		t.Fatalf("decode must not fail on a project pin: %v", err)
	}
}

func TestProjectFooterIsStableWithoutAProject(t *testing.T) {
	s := New("http://127.0.0.1:1")
	if got := s.projectFooter(); got != "" {
		t.Fatalf("footer = %q, want empty when no project is known", got)
	}
	if _, err := s.Call("prune_history", map[string]any{"hosts": "x.example", "expectedProject": fmt.Sprint(1)}); err == nil {
		t.Fatal("expected the unreachable control plane to fail the call")
	}
}
