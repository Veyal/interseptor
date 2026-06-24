package intruder

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// Spec.ExtraFlags are OR'd onto every send the attack records, so an AI-driven
// intruder run (FlagIntruder | FlagAI) is recognizable and can surface in History.
func TestSpecExtraFlagsTagsSends(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	e := New(sender.New(s, capture.New(s)))

	if err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET /x?q=§seed§ HTTP/1.1\nHost: h\n\n",
		AttackType: "sniper",
		Payloads:   [][]string{{"a", "b"}},
		ExtraFlags: store.FlagAI,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, e)

	flows, err := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagIntruder, Limit: 50})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("no intruder flows recorded")
	}
	for _, f := range flows {
		if f.Flags&store.FlagAI == 0 {
			t.Fatalf("intruder flow missing FlagAI, flags=%d", f.Flags)
		}
	}
}
