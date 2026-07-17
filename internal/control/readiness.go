package control

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// readinessCheck is one row in the agent setup checklist.
type readinessCheck struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// readinessReport is the structured pre-flight result for MCP and UI.
type readinessReport struct {
	Ready    bool             `json:"ready"`
	Checks   []readinessCheck `json:"checks"`
	Blockers []string         `json:"blockers"`
}

// buildReadiness stays on authzAPI (not metaAPI, despite /api/readiness being a
// general pre-flight endpoint): it calls h.authzIdentities() to populate the
// "auth_identities" checklist row, and authzIdentities is core authz-domain logic
// (also used by getAuthz/authzRun/authzPromoteFromFlow in authz.go) — pulling it
// out onto a neutral receiver would mean either duplicating that logic or adding a
// cross-group call, which is more invasive than the cosmetic grouping fix is worth.
// The route itself is still registered in registerAuthzRoutes for the same reason.
func (h *authzAPI) buildReadiness() readinessReport {
	var rep readinessReport
	add := func(id string, ok bool, detail, fix string) {
		rep.Checks = append(rep.Checks, readinessCheck{ID: id, OK: ok, Detail: detail, Fix: fix})
		if !ok {
			rep.Blockers = append(rep.Blockers, id)
		}
	}

	proxyAddr := h.currentProxyAddr()
	deviceEP := h.resolveDeviceEndpoint()
	proxyDetail := "listening " + proxyAddr
	if deviceEP.Endpoint != "" && deviceEP.Endpoint != proxyAddr {
		proxyDetail += " · device proxy " + deviceEP.Endpoint
	}
	add("proxy", proxyAddr != "", proxyDetail, "start Interseptor or check bind address in Settings")

	includes := 0
	if rules, err := h.st.ListScopeRules(); err == nil {
		for _, r := range rules {
			if r.Enabled && r.Action == "include" {
				includes++
			}
		}
	}
	if includes > 0 {
		add("scope", true, itoa64(int64(includes))+" include rule(s)", "")
	} else {
		add("scope", false, "no enabled include rules", "scope_from_url to focus on the target")
	}

	provider, _, _, aiOK := h.aiCreds()
	aiDetail := "not configured"
	if aiOK {
		aiDetail = provider + " configured"
	}
	add("ai_provider", aiOK, aiDetail, "configure an AI provider and API key in Settings")

	flowN, _ := h.st.FlowCount()
	trafficOK := flowN > 0
	add("traffic", trafficOK, flowCountDetail(flowN), "route the target through the proxy and browse the app")

	inScopeOK := true
	if includes > 0 {
		inScopeOK = h.hasInScopeTraffic()
		add("in_scope_traffic", inScopeOK, inScopeDetail(inScopeOK), "browse in-scope hosts or relax scope rules")
	}

	oobOK := h.oobEnabled()
	oobDetail := "enabled"
	oobFix := ""
	if !oobOK {
		oobDetail = "disabled"
		oobFix = "oob_enable or Settings → enable OOB"
	}
	add("oob", oobOK, oobDetail, oobFix)

	ids := h.authzIdentities()
	authN := 0
	for _, id := range ids {
		if identityHasAuth(id) && !id.Broken {
			authN++
		}
	}
	add("auth_identities", authN > 0, itoa64(int64(authN))+" identities", "promote_flow_to_authz or set_authz")

	lm := h.loadLoginMacro()
	loginOK := lm.Enabled && lm.Request != ""
	loginDetail := "not configured"
	if loginOK {
		loginDetail = "configured for " + lm.Target
	}
	add("login_macro", loginOK, loginDetail, "set_login_macro_from_flow or set_login_macro")

	h.as.mu.Lock()
	armed := h.as.armed
	h.as.mu.Unlock()
	add("active_scan_armed", armed, armedStatus(armed), "active_scan with arm:true once authorized")

	tlsDiag := h.buildTLSDiagnosis("")
	tlsOK := tlsDiag.Verdict == "ok" || tlsDiag.Verdict == "no_https"
	tlsDetail := tlsDiag.Detail
	tlsFix := tlsDiag.Fix
	if tlsDiag.Verdict == "tls_blocked" {
		tlsDetail = fmt.Sprintf("%d TLS rejection(s) — pinning or untrusted CA", tlsDiag.TLSFailureCount)
	}
	add("tls_intercept", tlsOK, tlsDetail, tlsFix)

	rep.Ready = proxyAddr != "" && trafficOK && inScopeOK && tlsOK
	return rep
}

func (h *authzAPI) getReadiness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.buildReadiness())
}

func flowCountDetail(n int64) string {
	if n == 0 {
		return "no flows captured"
	}
	return itoa64(n) + " flows captured"
}

func inScopeDetail(ok bool) string {
	if ok {
		return "in-scope traffic present"
	}
	return "no in-scope traffic yet"
}

func armedStatus(armed bool) string {
	if armed {
		return "armed"
	}
	return "disarmed"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func readinessText(rep readinessReport) string {
	summary := "ready"
	if !rep.Ready {
		summary = "not ready"
	}
	raw, _ := json.MarshalIndent(rep, "", "  ")
	return summary + " — structured checklist:\n" + string(raw)
}
