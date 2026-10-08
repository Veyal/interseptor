package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/version"
)

// GET /api/mcp and GET /api/mcp/capabilities must advertise the generated agent guide.
func TestMCPEndpointsAdvertiseAgentDocs(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, path := range []string{"/api/mcp", "/api/mcp/capabilities"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		var body struct {
			Documentation map[string]string `json:"documentation"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if body.Documentation["llmsTxt"] != version.LLMSURL || body.Documentation["llmsFullTxt"] != version.LLMSFullURL || body.Documentation["agentGuide"] != version.AgentGuideURL {
			t.Fatalf("%s documentation = %#v", path, body.Documentation)
		}
	}
}

func TestRouteCatalogMatchesReferenceRoutes(t *testing.T) {
	got := RouteCatalog()
	if len(got) != len(apiRoutes) || len(got) == 0 {
		t.Fatalf("RouteCatalog has %d routes, apiRoutes has %d", len(got), len(apiRoutes))
	}
	got[0].Path = "mutated"
	if apiRoutes[0].Path == "mutated" {
		t.Fatal("RouteCatalog must return a copy")
	}
}
