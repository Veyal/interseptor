package control

import (
	"strings"
	"testing"
)

// The Android status reports the proxy of the device it resolved; the UI must
// ask for the selected device and only call the proxy active when the reported
// serial is that device, never another connected one.
func TestUIAndroidProxyStateBelongsToSelectedDevice(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	start := strings.Index(settings, "export async function loadAndroid(")
	end := strings.Index(settings, "async function androidAction(fn)")
	if start < 0 || end <= start {
		t.Fatal("loadAndroid boundary not found")
	}
	loader := settings[start:end]
	for _, want := range []string{
		"'?serial='+encodeURIComponent(requested)",
		"s.proxySerial===selSerial&&s.proxyActive",
	} {
		if !strings.Contains(loader, want) {
			t.Errorf("loadAndroid missing %q", want)
		}
	}
	if strings.Contains(loader, "(s.proxyActive?s.proxyValue:'')") {
		t.Error("loadAndroid must not report another device's proxy as active")
	}
}
