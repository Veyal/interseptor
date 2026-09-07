package control

import (
	"strings"
	"testing"
)

func TestUISessionInspectionUsesPassiveEndpointAndCustomRoleControls(t *testing.T) {
	index := readUIAsset(t, "index.html")
	js := readUIAsset(t, "js/session-inspection.js")
	proxy := readUIAsset(t, "js/proxy.js")
	if !strings.Contains(index, `id="sessionInspectModal"`) || strings.Contains(index[strings.Index(index, `id="sessionInspectModal"`):strings.Index(index, `id="sessionInspectModal"`)+1600], "<select") {
		t.Fatal("session inspection must be a custom modal without native select controls")
	}
	for _, marker := range []string{
		"/api/flows/session-inspect?",
		"data-session-role",
		"browserDecision",
		"responseCookies",
		"transitions",
		"observations",
		"No requests are sent",
	} {
		if !strings.Contains(index+js, marker) {
			t.Errorf("session inspection marker %q missing", marker)
		}
	}
	if strings.Contains(js, "/replay/") || strings.Contains(js, "repeater/send") || strings.Contains(js, "fetch('/api/flows/") {
		t.Fatal("session inspection must not add request sending or replay")
	}
	if !strings.Contains(proxy, "Inspect session timeline") || !strings.Contains(proxy, "openSessionInspector") {
		t.Fatal("History menu is not connected to passive session inspection")
	}
}
