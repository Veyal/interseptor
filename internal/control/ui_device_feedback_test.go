package control

import (
	"strings"
	"testing"
)

func TestUIAndroidActionsRequireReadyDevice(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, contract := range []string{
		"function setAndroidDeviceActionsEnabled(enabled)",
		"'androidSetupAllBtn'",
		"'androidInstallUserBtn'",
		"'androidInstallSystemBtn'",
		"'androidProxyBtn'",
		"'androidUnproxyBtn'",
		"setAndroidDeviceActionsEnabled(devs.some(d=>d.state==='device')&&!!androidSerial())",
		"setAndroidDeviceActionsEnabled(false)",
	} {
		if !strings.Contains(settings, contract) {
			t.Errorf("Android no-device safety contract missing %q", contract)
		}
	}
}

func TestUIIOSSurfacesDeviceDiscoveryError(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, contract := range []string{
		"s.deviceError",
		"Device discovery failed:",
		"Install or select Xcode",
	} {
		if !strings.Contains(settings, contract) {
			t.Errorf("iOS discovery feedback contract missing %q", contract)
		}
	}
}
