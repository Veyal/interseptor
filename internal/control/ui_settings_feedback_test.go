package control

import (
	"regexp"
	"strings"
	"testing"
)

// A failed Settings save must announce itself as an error (role=alert, error
// styling), not as an informational toast that looks like success copy. Every
// toast that reports e.message/err.message passes the 'error' severity.
func TestUISettingsFailureToastsUseErrorSeverity(t *testing.T) {
	failure := regexp.MustCompile(`toast\([^;]*?(?:\be|\berr)\.message\)`)
	for _, name := range []string{"js/settings.js", "js/settings-appearance.js", "js/settings-health.js"} {
		src := executableJS(readUIAsset(t, name))
		for _, m := range failure.FindAllString(src, -1) {
			t.Errorf("%s reports a failure without error severity: %s", name, m)
		}
	}
	src := executableJS(readUIAsset(t, "js/settings.js"))
	for _, msg := range []string{
		"toast('Proxy authentication needs a username and password','error')",
		"toast('enter at least one listener','error')",
		"toast('enter control host and port','error')",
		"toast('at least one proxy listener required','error')",
	} {
		if !strings.Contains(src, msg) {
			t.Errorf("settings.js validation message must use error severity: %s", msg)
		}
	}
}
