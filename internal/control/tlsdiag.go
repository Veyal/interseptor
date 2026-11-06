package control

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// humanAge renders a coarse age ("45s", "12m", "3h12m", "2d4h") for diagnostics.
func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// tlsFailureSummary is one recorded CONNECT→TLS-failure for the diagnosis report.
type tlsFailureSummary struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	ClientAddr string `json:"clientAddr"`
	Error      string `json:"error"`
}

// tlsDiagnosisReport helps operators tell pinning/CA rejection from silent proxy bypass.
type tlsDiagnosisReport struct {
	Verdict         string              `json:"verdict"`
	Detail          string              `json:"detail"`
	Fix             string              `json:"fix,omitempty"`
	CanBypass       bool                `json:"canBypass"` // always false — Interseptor is a proxy, not a pin bypass tool
	BypassHints     []string            `json:"bypassHints,omitempty"`
	TLSFailureCount int64               `json:"tlsFailureCount"`
	// RecentFailureCount counts rejections inside tlsFailureWindow; a stale
	// rejection must not keep the checklist red after interception started
	// working. NewestFailureAge is human-readable ("3h12m") and empty when there
	// has never been a rejection.
	RecentFailureCount int64               `json:"recentFailureCount"`
	NewestFailureTS    int64               `json:"newestFailureTs,omitempty"`
	NewestFailureAge   string              `json:"newestFailureAge,omitempty"`
	HTTPSOkCount    int64               `json:"httpsOkCount"`
	TotalFlows      int64               `json:"totalFlows"`
	RecentFailures  []tlsFailureSummary `json:"recentFailures,omitempty"`
	HostsBlocked    []string            `json:"hostsBlocked,omitempty"`
}

// tlsFailureWindow is how recent a rejection must be to count as current. A
// failure older than this is reported with its age instead of blocking the
// readiness check, so a fixed environment stops looking broken. A var so tests
// can shorten it.
var tlsFailureWindow = 15 * time.Minute

var tlsBypassHints = []string{
	"Interseptor cannot bypass SSL pinning — it only detects when the app rejects the MITM certificate.",
	"Frida / objection at runtime (hook OkHttp CertificatePinner, TrustManager, etc.)",
	"Repack the APK: disable pinning in network_security_config or patch smali",
	"Emulator with system CA (Settings → Android → Setup all) — works only if the app does not pin",
	"Rooted device + Magisk + LSPosed modules, or a dedicated pentest device image",
}

// buildTLSDiagnosis stays on authzAPI (not metaAPI) purely so it shares a receiver
// with buildReadiness in readiness.go, which calls h.buildTLSDiagnosis("") directly
// to populate the "tls_intercept" checklist row. buildTLSDiagnosis itself has no
// authz-specific dependency (it only reads h.st flow/TLS-failure data) — if
// readiness.go's authzIdentities() coupling is ever removed, this can move to
// metaAPI alongside it. See readiness.go for the fuller rationale on why
// /api/readiness and /api/tls-diagnosis are colocated with authz for now.
func (h *authzAPI) buildTLSDiagnosis(hostFilter string) tlsDiagnosisReport {
	hostFilter = strings.TrimSpace(hostFilter)
	rep := tlsDiagnosisReport{Verdict: "ok", CanBypass: false, BypassHints: tlsBypassHints}

	failures, _ := h.st.QueryFlowsFilter(store.FlowFilter{
		RequireFlags: store.FlagTLSFailed,
		Host:         hostFilter,
		Limit:        50,
		SortKey:      "id",
		SortDir:      -1,
	})
	rep.TLSFailureCount, _ = h.st.CountFlowsWithFlag(store.FlagTLSFailed)
	if hostFilter != "" {
		rep.TLSFailureCount = int64(len(failures))
		for _, f := range failures {
			rep.RecentFailures = append(rep.RecentFailures, tlsFailureSummary{
				ID: f.ID, TS: f.TS.UnixMilli(), Host: f.Host, Port: f.Port,
				ClientAddr: f.ClientAddr, Error: f.Error,
			})
		}
	} else {
		for _, f := range failures {
			rep.RecentFailures = append(rep.RecentFailures, tlsFailureSummary{
				ID: f.ID, TS: f.TS.UnixMilli(), Host: f.Host, Port: f.Port,
				ClientAddr: f.ClientAddr, Error: f.Error,
			})
		}
	}
	rep.HTTPSOkCount, _ = h.st.CountSuccessfulHTTPS(hostFilter)
	rep.TotalFlows, _ = h.st.FlowCount()

	hostSet := map[string]struct{}{}
	for _, f := range failures {
		hostSet[f.Host] = struct{}{}
	}
	for hst := range hostSet {
		rep.HostsBlocked = append(rep.HostsBlocked, hst)
	}

	newestFailureMS, recentFailures, newestSuccessMS, _ := h.st.TLSFailureStats(hostFilter, time.Now().Add(-tlsFailureWindow).UnixMilli())
	rep.RecentFailureCount = recentFailures
	rep.NewestFailureTS = newestFailureMS
	if newestFailureMS > 0 {
		rep.NewestFailureAge = humanAge(time.Since(time.UnixMilli(newestFailureMS)))
	}

	switch {
	case rep.TLSFailureCount > 0 && rep.HTTPSOkCount > 0 && newestSuccessMS > newestFailureMS:
		// A successful MITM is newer than the last rejection: interception works
		// now, whatever the history says. Without this, a fixed environment keeps
		// reporting a blocker until the old rows age out of the window, and a real
		// regression hides in that noise.
		rep.Verdict = "ok"
		rep.Detail = fmt.Sprintf("HTTPS interception is working (%d successful HTTPS flows, newest %s ago; the last rejection was %s ago and is historical)",
			rep.HTTPSOkCount, humanAge(time.Since(time.UnixMilli(newestSuccessMS))), rep.NewestFailureAge)
	case rep.TLSFailureCount > 0 && recentFailures > 0:
		rep.Verdict = "tls_blocked"
		n := rep.TLSFailureCount
		if hostFilter != "" {
			n = int64(len(failures))
		}
		rep.Detail = fmt.Sprintf("%d CONNECT tunnel(s) reached the proxy and the client rejected the MITM certificate before any HTTP request (newest %s ago)", n, rep.NewestFailureAge)
		rep.Fix = "The app likely uses SSL pinning or ignores your CA. Bypass with Frida/objection, a patched APK, or an emulator with a system CA (android_setup caMode:system). On Android 7+, user CAs are ignored by most apps — use system CA or patch network_security_config."
	case rep.TLSFailureCount > 0 && rep.HTTPSOkCount > 0:
		// Historical rejections only: interception is demonstrably working now
		// (the pinning bypass landed), so report the age instead of blocking.
		rep.Verdict = "ok"
		rep.Detail = fmt.Sprintf("HTTPS interception is working (%d successful HTTPS flows); %d older TLS rejection(s), newest %s ago — no rejection in the last %s",
			rep.HTTPSOkCount, rep.TLSFailureCount, rep.NewestFailureAge, humanAge(tlsFailureWindow))
		_ = newestSuccessMS
	case rep.TLSFailureCount > 0:
		rep.Verdict = "tls_blocked"
		rep.Detail = fmt.Sprintf("%d CONNECT tunnel(s) reached the proxy but no HTTPS flow has been intercepted yet (newest rejection %s ago)", rep.TLSFailureCount, rep.NewestFailureAge)
		rep.Fix = "The app likely uses SSL pinning or ignores your CA. Bypass with Frida/objection, a patched APK, or an emulator with a system CA (android_setup caMode:system). On Android 7+, user CAs are ignored by most apps — use system CA or patch network_security_config."
	case rep.TotalFlows == 0:
		rep.Verdict = "no_traffic"
		rep.Detail = "No traffic reached the proxy — the request may not have been sent, or the app bypasses the system proxy"
		rep.Fix = "Confirm the device proxy (Settings → TLS, or android_setup). Some mobile apps ignore the proxy and connect directly; try an emulator with adb reverse or patch the app."
	case rep.HTTPSOkCount == 0 && rep.TotalFlows > 0:
		rep.Verdict = "no_https"
		rep.Detail = "Traffic was captured but no HTTPS was successfully intercepted — only cleartext HTTP, or HTTPS never went through the proxy"
		rep.Fix = "Use the app normally to trigger API calls. If TLS failures appear afterward, treat as pinning (tls_blocked)."
	default:
		rep.Detail = fmt.Sprintf("HTTPS interception is working (%d successful HTTPS flows, %d total)", rep.HTTPSOkCount, rep.TotalFlows)
	}
	return rep
}

func (h *authzAPI) getTLSDiagnosis(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.buildTLSDiagnosis(r.URL.Query().Get("host")))
}

func tlsDiagnosisText(rep tlsDiagnosisReport) string {
	return rep.Verdict + " — " + rep.Detail
}
