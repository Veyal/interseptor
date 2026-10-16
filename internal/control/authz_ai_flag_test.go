package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// authzFlowsWithFlag returns every stored flow carrying store.FlagAuthz.
func authzFlowsWithFlag(t *testing.T, st *store.Store) []*store.Flow {
	t.Helper()
	all, err := st.QueryFlows(500)
	if err != nil {
		t.Fatal(err)
	}
	var out []*store.Flow
	for _, f := range all {
		if f.Flags&store.FlagAuthz != 0 {
			out = append(out, f)
		}
	}
	return out
}

// TestAuthzReplayFlowsCarryAISource drives every authz send endpoint with and
// without X-Interseptor-Source: ai and checks the recorded flows' flags.
func TestAuthzReplayFlowsCarryAISource(t *testing.T) {
	endpoints := []struct {
		name, path string
		body       func(flowID int64) string
	}{
		{"run", "/api/authz/run", func(id int64) string { return `{"flowId":` + itoa64(id) + `}` }},
		{"check-sessions", "/api/authz/check-sessions", func(id int64) string { return `{"flowId":` + itoa64(id) + `}` }},
		{"differential", "/api/authz/differential", func(id int64) string {
			return `{"flowId":` + itoa64(id) + `,"invalidBody":"bad"}`
		}},
		{"cross-host-replay", "/api/authz/cross-host-replay", func(id int64) string {
			return `{"flowId":` + itoa64(id) + `,"jwt":"a.b.c"}`
		}},
	}
	for _, ep := range endpoints {
		for _, ai := range []bool{true, false} {
			name := ep.name + "/human"
			if ai {
				name = ep.name + "/ai"
			}
			t.Run(name, func(t *testing.T) {
				var state atomic.Int64
				_, f := differentialTarget(t, &state)
				h, st, _ := newHub(t)
				ts := httptest.NewServer(h.Handler())
				defer ts.Close()
				flowID, err := st.InsertFlow(f)
				if err != nil {
					t.Fatal(err)
				}
				authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"}]}`)

				req, err := http.NewRequest(http.MethodPost, ts.URL+ep.path, strings.NewReader(ep.body(flowID)))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				if ai {
					req.Header.Set("X-Interseptor-Source", "ai")
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}

				flows := authzFlowsWithFlag(t, st)
				if len(flows) == 0 {
					t.Fatal("no FlagAuthz flows recorded")
				}
				for _, fl := range flows {
					if got := fl.Flags&store.FlagAI != 0; got != ai {
						t.Errorf("flow %d flags=%#x: FlagAI=%v, want %v", fl.ID, fl.Flags, got, ai)
					}
				}
			})
		}
	}
}
