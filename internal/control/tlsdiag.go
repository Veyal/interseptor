package control

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Veyal/interceptor/internal/store"
)

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
	CanBypass       bool                `json:"canBypass"` // always false — Interceptor is a proxy, not a pin bypass tool
	BypassHints     []string            `json:"bypassHints,omitempty"`
	TLSFailureCount int64               `json:"tlsFailureCount"`
	HTTPSOkCount    int64               `json:"httpsOkCount"`
	TotalFlows      int64               `json:"totalFlows"`
	RecentFailures  []tlsFailureSummary `json:"recentFailures,omitempty"`
	HostsBlocked    []string            `json:"hostsBlocked,omitempty"`
}

var tlsBypassHints = []string{
	"Interceptor cannot bypass SSL pinning — it only detects when the app rejects the MITM certificate.",
	"Frida / objection at runtime (hook OkHttp CertificatePinner, TrustManager, etc.)",
	"Repack the APK: disable pinning in network_security_config or patch smali",
	"Emulator with system CA (Settings → Android → Setup all) — works only if the app does not pin",
	"Rooted device + Magisk + LSPosed modules, or a dedicated pentest device image",
}

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

	switch {
	case rep.TLSFailureCount > 0:
		rep.Verdict = "tls_blocked"
		n := rep.TLSFailureCount
		if hostFilter != "" {
			n = int64(len(failures))
		}
		rep.Detail = fmt.Sprintf("%d CONNECT tunnel(s) reached the proxy but the client rejected the MITM certificate before any HTTP request", n)
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
