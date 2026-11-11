package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The operator's editor sends the whole block list. An agent that adds evidence
// over MCP while the operator types would have its block dropped by the next
// autosave, silently. A save that names the version it rendered from is refused
// with 409 and the current finding, so the operator decides.
func TestFindingSaveNamesItsBaseVersion(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	var created map[string]any
	post := func(body string) *http.Response {
		t.Helper()
		resp, err := http.Post(ts.URL+"/api/findings", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	post(`{"title":"Operator and agent","severity":"High","summary":"s"}`)
	id := int64(created["id"].(float64))
	base := int64(created["updatedTs"].(float64))
	if base == 0 {
		t.Fatal("the create response has no updatedTs to pin a save to")
	}

	patch := func(body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/findings/"+jsonInt(id), strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// An agent adds a block first (unconditional, as its tool does today).
	if code, out := patch(`{"blocks":[{"type":"text","md":"AGENT ADDED RESULT"}]}`); code != http.StatusOK {
		t.Fatalf("agent patch: %d %v", code, out)
	}
	// The operator's autosave still names the version it rendered from.
	code, out := patch(`{"blocks":[{"type":"text","md":"operator step"}],"baseUpdatedTs":` + jsonInt(base) + `}`)
	if code != http.StatusConflict {
		t.Fatalf("stale save: %d %v, want 409", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "not saved") {
		t.Fatalf("conflict message = %q", msg)
	}
	conflictFinding, _ := out["finding"].(map[string]any)
	if conflictFinding == nil {
		t.Fatal("the 409 must carry the current finding so the client can refresh its base")
	}
	// The agent's block survived.
	resp, err := http.Get(ts.URL + "/api/findings/" + jsonInt(id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stored map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if got := bodyText(stored); !strings.Contains(got, "AGENT ADDED RESULT") || strings.Contains(got, "operator step") {
		t.Fatalf("stored body = %q, want the agent's block and not the refused save", got)
	}

	// Saving from the current version works, and a save without a base keeps the
	// unconditional behaviour the MCP tools and imports rely on.
	now := int64(stored["updatedTs"].(float64))
	if code, out := patch(`{"blocks":[{"type":"text","md":"operator step"}],"baseUpdatedTs":` + jsonInt(now) + `}`); code != http.StatusOK {
		t.Fatalf("save from the current version: %d %v", code, out)
	}
	if code, out := patch(`{"blocks":[{"type":"text","md":"unconditional"}]}`); code != http.StatusOK {
		t.Fatalf("save without a base: %d %v", code, out)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// bodyText returns the text the finding's blocks carry, for assertions.
func bodyText(f map[string]any) string {
	var parts []string
	blocks, _ := f["blocks"].([]any)
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if s, ok := m["md"].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		if s, ok := f["body"].(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}
