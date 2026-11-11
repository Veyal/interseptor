package control

import (
	"os"
	"strings"
	"testing"
)

// The server can start in the default project when the remembered external
// folder is unavailable, and GET /api/project then carries startupNotice. The
// web UI has to say so; before this the reason reached only the log and the
// desktop shell.
func TestUIExplainsWhyAFallbackProjectWasOpened(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile("ui/" + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(b)
	}
	index := read("index.html")
	if !strings.Contains(index, `<div id="startupNotice" class="startup-notice" role="status" aria-live="polite" hidden></div>`) {
		t.Fatal("index.html needs a shell-level #startupNotice region")
	}
	// It is a status, not an alert: the server still started and works.
	if strings.Contains(index, `id="startupNotice" class="startup-notice" role="alert"`) {
		t.Fatal("the notice is informational; role=alert would interrupt")
	}
	settings := read("js/settings.js")
	if !strings.Contains(settings, "syncStartupNotice(d);") {
		t.Fatal("loadProject must hand its payload to the notice: it already has startupNotice, so this costs no request")
	}
	notice := read("js/startup-notice.js")
	for _, want := range []string{"payload.startupNotice", "esc(startupNoticeMessage(", "localStorage.setItem(DISMISSED_KEY"} {
		if !strings.Contains(notice, want) {
			t.Errorf("startup-notice.js is missing %q", want)
		}
	}
	// The reason is server text: it must reach the page escaped, never as HTML.
	if !strings.Contains(notice, "startupNoticeMessage(decision.text))}") {
		t.Error("the message must be inserted through esc()")
	}
	if !strings.Contains(read("panel-proxy.css"), ".startup-notice-text{") {
		t.Error("no styling for the notice")
	}
}
