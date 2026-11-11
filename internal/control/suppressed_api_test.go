package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/proxy"
)

func TestSuppressedEndpointReportsDroppedTraffic(t *testing.T) {
	h, _, _ := newHub(t)
	h.SetSuppressedProvider(func(window time.Duration) proxy.SuppressedReport {
		return proxy.SuppressedReport{
			WindowSec: int(window.Seconds()), Total: 3,
			Hosts:   []proxy.SuppressedHostStat{{Host: "pos.example", Reason: proxy.SuppressedOutOfScope, Count: 3, NewestTS: time.Now().UnixMilli()}},
			Samples: []proxy.SuppressedRequest{{Host: "pos.example", Method: "GET", Path: "/screen", Reason: proxy.SuppressedOutOfScope}},
		}
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows/suppressed?windowSec=300")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var rep proxy.SuppressedReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Total != 3 || rep.WindowSec != 300 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Hosts) != 1 || rep.Hosts[0].Host != "pos.example" {
		t.Fatalf("hosts = %+v", rep.Hosts)
	}
}

// An empty History plus a filtered request body must read as "we dropped it",
// not "the app sent nothing".
func TestReadinessTrafficNamesSuppressedTraffic(t *testing.T) {
	h, _, _ := newHub(t)
	h.SetSuppressedProvider(func(time.Duration) proxy.SuppressedReport {
		return proxy.SuppressedReport{Total: 12, Hosts: []proxy.SuppressedHostStat{{Host: "pos.example", Reason: proxy.SuppressedOutOfScope, Count: 12}}}
	})
	rep := (&authzAPI{h}).buildReadiness()
	for _, c := range rep.Checks {
		if c.ID != "traffic" {
			continue
		}
		if !strings.Contains(c.Detail, "12 suppressed by filters") || !strings.Contains(c.Detail, "pos.example") {
			t.Fatalf("traffic detail = %q", c.Detail)
		}
		return
	}
	t.Fatal("traffic row missing")
}
