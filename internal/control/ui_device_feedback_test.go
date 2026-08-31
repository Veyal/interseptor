package control

import (
	"strings"
	"testing"
)

func TestUIAndroidActionsRequireReadyDevice(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, contract := range []string{
		"let androidDeviceActionsReady=false",
		"function setAndroidDeviceActionsEnabled(enabled)",
		"'androidSetupAllBtn'",
		"'androidInstallUserBtn'",
		"'androidInstallSystemBtn'",
		"'androidProxyBtn'",
		"'androidUnproxyBtn'",
		"androidDeviceActionsReady=devs.some(d=>d.state==='device')&&!!androidSerial()",
		"setAndroidDeviceActionsEnabled(androidDeviceActionsReady)",
		"return {ok:true,ready:androidDeviceActionsReady}",
		"return {ok:false,ready:false}",
		"const status=await loadAndroid({allowDuringAction:true})",
		"if(error||!status?.ok)return false",
		"setAndroidDeviceActionsEnabled(false)",
	} {
		if !strings.Contains(settings, contract) {
			t.Errorf("Android no-device safety contract missing %q", contract)
		}
	}
}

func TestUIMobileActionBusyUsesValidARIATokens(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, contract := range []string{
		"function setAriaBusy(control,busy)",
		"control.setAttribute('aria-busy','true')",
		"control.removeAttribute('aria-busy')",
	} {
		if !strings.Contains(settings, contract) {
			t.Errorf("mobile pending-state contract missing %q", contract)
		}
	}
	if strings.Contains(settings, "toggleAttribute('aria-busy'") {
		t.Error("mobile pending controls must use valid true tokens and remove aria-busy when idle")
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
