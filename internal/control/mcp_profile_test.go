package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
)

// POST /mcp?profile=<name> must serve that profile's tools, and only those.
func TestMCPEndpointHonoursProfileQuery(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	list := func(query string) []string {
		t.Helper()
		resp, err := http.Post(ts.URL+"/mcp"+query, "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatalf("POST /mcp%s: %v", query, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /mcp%s status = %d, want 200", query, resp.StatusCode)
		}
		var out struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode tools/list: %v", err)
		}
		names := make([]string, 0, len(out.Result.Tools))
		for _, tool := range out.Result.Tools {
			names = append(names, tool.Name)
		}
		return names
	}

	full := list("")
	if len(full) != len(mcp.ProfileTools(mcp.ProfileFull)) {
		t.Fatalf("default /mcp served %d tools, want the full %d", len(full), len(mcp.ProfileTools(mcp.ProfileFull)))
	}
	web := list("?profile=web")
	if len(web) != len(mcp.ProfileTools(mcp.ProfileWebPentest)) {
		t.Fatalf("?profile=web served %d tools, want %d", len(web), len(mcp.ProfileTools(mcp.ProfileWebPentest)))
	}
	for _, name := range web {
		if name == "drop_request" {
			t.Error("?profile=web served a packet-holding tool")
		}
	}
	if n := len(list("?profile=mobile")); n != len(mcp.ProfileTools(mcp.ProfileMobile)) {
		t.Fatalf("?profile=mobile served %d tools, want %d", n, len(mcp.ProfileTools(mcp.ProfileMobile)))
	}
}

// An unknown profile is a 400 naming the valid profiles, not a silent fallback
// to the full surface.
func TestMCPEndpointRejectsUnknownProfile(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mcp?profile=nonsense", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// With no ?profile=, the environment decides — that is how a stdio-style
// deployment pins a profile for an HTTP client it does not control.
func TestMCPEndpointFallsBackToTheEnvironmentProfile(t *testing.T) {
	t.Setenv(mcp.ProfileEnvVar, "reporting")
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Profile string `json:"profile"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if out.Result.Profile != string(mcp.ProfileReporting) {
		t.Fatalf("initialize profile = %q, want %q", out.Result.Profile, mcp.ProfileReporting)
	}
}

// GET /api/mcp must advertise the profile catalogue so the UI and an agent can
// discover the names without reading the source.
func TestMCPDescriptorAdvertisesProfiles(t *testing.T) {
	got := mcpDescriptorForRequest("127.0.0.1:9966")
	profiles, ok := got["profiles"].([]map[string]any)
	if !ok || len(profiles) != len(mcp.Profiles()) {
		t.Fatalf("descriptor profiles = %#v, want %d entries", got["profiles"], len(mcp.Profiles()))
	}
	var sawDefault bool
	for _, p := range profiles {
		if p["default"] == true {
			if p["name"] != string(mcp.ProfileFull) {
				t.Errorf("default profile = %v, want full", p["name"])
			}
			sawDefault = true
		}
		if p["desc"] == "" || p["tools"] == 0 {
			t.Errorf("profile entry is incomplete: %#v", p)
		}
	}
	if !sawDefault {
		t.Error("no profile is marked default")
	}
	note, _ := got["profileNote"].(string)
	if !strings.Contains(note, mcp.ProfileEnvVar) || !strings.Contains(note, "profile=") {
		t.Errorf("profileNote does not explain how to select a profile: %q", note)
	}
}
