package control

import (
	"strings"
	"testing"
)

func TestUIAuthTimelineIsReadOnlyHypothesisLabelledAndHidesValues(t *testing.T) {
	index := readUIAsset(t, "index.html")
	js := readUIAsset(t, "js/authtimeline.js")
	proxy := readUIAsset(t, "js/proxy.js")

	if !strings.Contains(index, `id="authTimelineModal"`) {
		t.Fatal("index.html is missing the auth timeline modal")
	}
	start := strings.Index(index, `id="authTimelineModal"`)
	if strings.Contains(index[start:start+1800], "<select") {
		t.Fatal("auth timeline must not use native select controls")
	}
	for _, marker := range []string{
		"/auth-timeline",
		"lostAt",
		"hypothesis",
		"fingerprint",
		"data-auth-open-flow",
		"No requests are sent",
	} {
		if !strings.Contains(index+js, marker) {
			t.Errorf("auth timeline marker %q missing", marker)
		}
	}
	exec := executableJS(js)
	if strings.Contains(exec, "/replay/") || strings.Contains(exec, "repeater/send") || strings.Contains(exec, "method:'POST'") || strings.Contains(exec, `method: 'POST'`) {
		t.Fatal("auth timeline must stay read-only (no replay or mutating requests)")
	}
	if !strings.Contains(proxy, "openAuthTimeline") || !strings.Contains(proxy, "Auth timeline") {
		t.Fatal("History menu is not connected to the auth timeline")
	}
}
