package proxy

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// A handshake failure for a host that has a pinning-bypass enabler recorded is a
// capture gap (pinning_blocked), not a missing interception; without an enabler
// the flow keeps only the ambiguous ssl-pinning? hint.
func TestTLSFailureAnnotatesPinningBlockedWhenEnablerRecorded(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	srv := New(st, capture.New(st), nil, nil, nil)
	req, _ := http.NewRequest(http.MethodConnect, "https://api.example.com:443", nil)

	record := func(host string) []string {
		srv.recordTLSFailure(host, 443, "127.0.0.1:1", req, time.Now(), errors.New("EOF"), srv.snapshotSuppression())
		flows, err := st.QueryFlows(1)
		if err != nil || len(flows) != 1 {
			t.Fatalf("flows=%v err=%v", flows, err)
		}
		tags, _ := st.FlowTags(flows[0].ID)
		return tags
	}
	has := func(tags []string, want string) bool {
		for _, tg := range tags {
			if tg == want {
				return true
			}
		}
		return false
	}

	if tags := record("api.example.com"); has(tags, store.AnnotationPinningBlocked) {
		t.Fatalf("no setup recorded but flow tagged: %v", tags)
	}
	if _, err := st.SetInterceptionSetup(store.InterceptionSetup{
		Hosts:    []string{"*.example.com"},
		Enablers: []store.InterceptionEnabler{{Tool: "frida", Method: "hook"}},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if tags := record("api.example.com"); !has(tags, store.AnnotationPinningBlocked) {
		t.Fatalf("enabler recorded but flow not tagged pinning_blocked: %v", tags)
	}
}
