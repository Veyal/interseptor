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

func TestUIIOSReconciliationPreservesDiscoveryFailures(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	iosLoadStart := strings.Index(settings, "export async function loadIOS(")
	iosActionStart := strings.Index(settings, "async function iosAction(fn)")
	iosActionEnd := strings.Index(settings, "function setIOSActionBusy(busy)")
	sshLoadStart := strings.Index(settings, "export async function loadIOSSsh(")
	sshActionStart := strings.Index(settings, "async function iosSshAction(fn)")
	sshActionEnd := strings.Index(settings, "function setIOSSshActionBusy(busy)")
	if iosLoadStart < 0 || iosActionStart <= iosLoadStart || iosActionEnd <= iosActionStart || sshLoadStart <= iosActionEnd || sshActionStart <= sshLoadStart || sshActionEnd <= sshActionStart {
		t.Fatal("iOS reconciliation boundaries not found")
	}
	iosLoad := settings[iosLoadStart:iosActionStart]
	iosAction := settings[iosActionStart:iosActionEnd]
	sshLoad := settings[sshLoadStart:sshActionStart]
	sshAction := settings[sshActionStart:sshActionEnd]
	for _, contract := range []struct {
		name   string
		body   string
		values []string
	}{
		{"iOS loader", iosLoad, []string{"return {ok:!s.deviceError}", "return {ok:false}"}},
		{"iOS action", iosAction, []string{"const status=await loadIOS({allowDuringAction:true})", "if(error||!status?.ok)return false", "await mobileReadiness('#iosHint')"}},
		{"iOS SSH loader", sshLoad, []string{"return {ok:true}", "return {ok:false}"}},
		{"iOS SSH action", sshAction, []string{"const status=await loadIOSSsh({allowDuringAction:true})", "if(error||!status?.ok)return false", "await mobileReadiness('#iosSshHint')"}},
	} {
		for _, value := range contract.values {
			if !strings.Contains(contract.body, value) {
				t.Errorf("%s missing %q", contract.name, value)
			}
		}
	}
	for name, action := range map[string]string{"iOS": iosAction, "iOS SSH": sshAction} {
		gate := strings.Index(action, "if(error||!status?.ok)return false")
		readiness := strings.Index(action, "await mobileReadiness(")
		if gate < 0 || readiness < gate {
			t.Errorf("%s action must reject failed reconciliation before readiness", name)
		}
	}
}
