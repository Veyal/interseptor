package control

import (
	"strings"
	"testing"
)

// The native app updates only the server it runs; the badge must not hand a
// remote, Homebrew or standalone server's update to the app.
func TestUIVersionBadgeHandsOffOnlyForDesktopInstalls(t *testing.T) {
	src := readUIAsset(t, "js/app.js")
	for _, want := range []string{
		"el.dataset.installMethod=d.installMethod||'';",
		"requestNative:()=>!!el&&el.dataset.installMethod==='desktop'&&requestNativeUpdateCheck(),",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}
