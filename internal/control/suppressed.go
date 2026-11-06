package control

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Veyal/interseptor/internal/proxy"
)

// suppressedProvider returns the proxy's recent drops. Set by cmd; nil in tests
// that do not exercise the capture filter.
type suppressedProvider func(window time.Duration) proxy.SuppressedReport

// SetSuppressedProvider wires the capture filter's ring into the API.
func (h *Hub) SetSuppressedProvider(fn suppressedProvider) { h.suppressedFn = fn }

// suppressedReport is what the API and the readiness pill read.
func (h *Hub) suppressedReport(window time.Duration) proxy.SuppressedReport {
	if h.suppressedFn == nil {
		return proxy.SuppressedReport{WindowSec: int(window.Seconds()), Hosts: []proxy.SuppressedHostStat{}, Samples: []proxy.SuppressedRequest{}}
	}
	return h.suppressedFn(window)
}

// getSuppressedFlows reports the requests the capture filter dropped.
//
//	GET /api/flows/suppressed?windowSec=900
//
// This is the answer to "the app sent nothing" that does not need the device:
// the proxy forwarded the requests and history has none of them because a filter
// dropped them.
func (h *flowAPI) getSuppressedFlows(w http.ResponseWriter, r *http.Request) {
	window := 15 * time.Minute
	if raw := r.URL.Query().Get("windowSec"); raw != "" {
		if n, err := parseIntParam(raw); err == nil && n > 0 && n <= 86400 {
			window = time.Duration(n) * time.Second
		}
	}
	writeJSON(w, http.StatusOK, h.suppressedReport(window))
}

// parseIntParam parses a non-negative integer query parameter.
func parseIntParam(raw string) (int, error) {
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", raw)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
