package intruder

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
)

// Spec.Guard is applied at dial time to every attack send, so a target whose
// resolved address is one of the tool's own listeners is refused.
func TestSpecGuardBlocksOwnListenerAtDial(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer upstream.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	e := newEngine(t)
	if err := e.Start(Spec{
		Target:     "http://localhost:" + portStr,
		Template:   "GET /x?q=§seed§ HTTP/1.1\nHost: h\n\n",
		AttackType: "sniper",
		Payloads:   [][]string{{"a"}},
		Guard:      &sender.IPGuard{OwnPorts: []int{port}},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if hits.Load() != 0 {
		t.Fatalf("guarded attack reached the own listener %d times", hits.Load())
	}
	if len(st.Results) != 1 || st.Results[0].Status != http.StatusBadGateway {
		t.Fatalf("want one 502 guard refusal, got %+v", st.Results)
	}
}
