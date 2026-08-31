package control

import (
	"strings"
	"testing"
)

func TestUIImmediateSettingsMutationsSerializeLatestIntent(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"const settingsMutationLanes=new Map()",
		"function queueSettingsMutation(key,value,options)",
		"async function drainSettingsMutation(key,lane)",
		"function settingsMutationValue(key,fallback,loadRevision)",
		"function saveBooleanSetting(key,value,options)",
		"let settingsMutationRevision=0",
		"const settingsAcknowledgedValues=new Map()",
		"const settingsRevision=settingsMutationRevision",
		"acknowledged.revision>loadRevision",
		"settingsAcknowledgedValues.set(key,{revision:++settingsMutationRevision,value:mutation.value})",
		"const latest=!lane.pending&&lane.active===mutation&&lane.generation===mutation.generation",
		"controls:new Map()",
		"lane.controls.set(control,!!control.disabled)",
		"lane.controls.forEach((wasDisabled,control)",
		"settingsMutationValue('originTLSVerify',!!s.originTLSVerify,settingsRevision)",
		"settingsMutationValue('oobEnabled',!!s.oobEnabled,settingsRevision)",
		"settingsMutationValue('captureScopeOnly',!!s.captureScopeOnly,settingsRevision)",
		"settingsMutationValue('suppressBrowserTelemetry',s.suppressBrowserTelemetry!==false,settingsRevision)",
		"settingsMutationValue('suppressAndroidTelemetry',s.suppressAndroidTelemetry!==false,settingsRevision)",
		"settingsMutationValue('invisibleProxy',!!s.invisibleProxy,settingsRevision)",
		"settingsMutationValue('autoBypassOnPinFailure',!!s.autoBypassOnPinFailure,settingsRevision)",
	)

	for _, key := range []string{
		"originTLSVerify",
		"oobEnabled",
		"captureScopeOnly",
		"suppressBrowserTelemetry",
		"suppressAndroidTelemetry",
		"invisibleProxy",
		"autoBypassOnPinFailure",
	} {
		if !strings.Contains(settings, "saveBooleanSetting('"+key+"'") {
			t.Errorf("immediate setting %q does not use the serialized ownership lane", key)
		}
	}
}

func TestUIAcknowledgedSettingsWritesInvalidateOlderRefreshes(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"function invalidateSettingsLoads({reconcile=true}={})",
		"function scheduleSettingsReconcile()",
		"export async function saveSettingsPatch(patch,{invalidate=true,reconcile=true}={})",
		"if(invalidate)invalidateSettingsLoads({reconcile})",
		"saveSettingsPatch({tlsBypassHosts:next})",
		"saveSettingsPatch({originTLSVerifyBypassHosts:next})",
		"saveSettingsPatch({upstreamProxy,upstreamProxyCA})",
		"saveSettingsPatch({proxyAddrs:addrs})",
		"saveSettingsPatch({controlAddr},{reconcile:false})",
		"let deviceProxyLoadEpoch=0",
		"let deviceProxyMutationPending=false",
		"const epoch=++deviceProxyLoadEpoch",
		"if(epoch!==deviceProxyLoadEpoch)return null",
		"if(deviceProxyMutationPending)return null",
		"deviceProxyMutationPending=true",
		"deviceProxyMutationPending=false",
		"if(!deviceProxyMutationPending){state.deviceProxy=s.deviceProxy||s.proxyAddr",
		"deviceProxyLoadEpoch++",
	)
	requireUIContracts(t, "js/settings.js", "list.value=savedHosts.join('\\n')")
	if strings.Contains(settings, "const mergeQueued=") {
		t.Error("explicit list saves must remain last-write-wins replacements, not silently restore removed hosts")
	}

	if got := strings.Count(settings, "api('/api/settings',{method:'PUT'"); got != 1 {
		t.Fatalf("all Settings PUTs must cross the shared acknowledgement boundary; found %d direct calls", got)
	}

	tlsdiag := requireUIContracts(t, "js/tlsdiag.js",
		"import('./settings.js')",
		"addTLSBypassHosts(hosts)",
	)
	if strings.Contains(tlsdiag, "api('/api/settings', { method: 'PUT'") {
		t.Error("TLS diagnosis bypass action must not write settings outside the shared ownership boundary")
	}
}

func TestUISessionFullWritesShareAtomicMutationLane(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"let sessionMutationTail=Promise.resolve()",
		"function queueSessionMutation(work)",
		"sessionMutationTail.catch(()=>{}).then(work)",
		"function writeSessionAll(body)",
		"return queueSessionMutation(()=>writeSessionAll(body))",
		"function runLoginMacroWithSession(body,onSaveAcknowledged)",
		"await writeSessionAll(body)",
		"if(onSaveAcknowledged)onSaveAcknowledged()",
		"return api('/api/session/login/run',{method:'POST'})",
	)
	if got := strings.Count(settings, "api('/api/session',{method:'POST'"); got != 1 {
		t.Fatalf("all full Session writes must cross one mutation lane; found %d POST call sites", got)
	}

	start := strings.Index(settings, "function runLoginMacroWithSession(body,onSaveAcknowledged)")
	end := strings.Index(settings[start:], "\n}")
	if start < 0 || end < 0 {
		t.Fatal("queued login-macro helper not found")
	}
	helper := settings[start : start+end]
	write := strings.Index(helper, "await writeSessionAll(body)")
	run := strings.Index(helper, "return api('/api/session/login/run'")
	if write < 0 || run < 0 || write > run {
		t.Error("Login Macro Run must save its queued snapshot before running it")
	}
}

func TestUISystemProxyRefreshCannotSupersedeItsMutation(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"let sysProxyLoadEpoch=0",
		"let sysProxyMutationPromise=null",
		"throwOnError=false",
		"if(throwOnError)throw e",
		"const epoch=++sysProxyLoadEpoch",
		"if(epoch!==sysProxyLoadEpoch)return",
		"export function setSystemProxyEnabled(enabled)",
		"catch(_){return sysProxyState;}",
		"sysProxyDesired=!!enabled",
		"if(!sysProxyMutationPromise)",
		"sysProxyLoadEpoch++",
		"sysProxyMutationPromise=null",
	)
	if got := strings.Count(settings, "api('/api/sysproxy',{method:'POST'"); got != 1 {
		t.Fatalf("system-proxy writes must have one shared owner; found %d POST call sites", got)
	}

	setup := requireUIContracts(t, "js/setup.js",
		"renderSetupSystemProxyError",
		"getSystemProxyStatus",
		"setSystemProxyEnabled",
		"getSystemProxyStatus({throwOnError:true})",
		"await setSystemProxyEnabled(true)",
		"acknowledged=await getSystemProxyStatus({render:false,throwOnError:true})",
	)
	if strings.Contains(setup, "api('/api/sysproxy', { method: 'POST'") {
		t.Error("Setup must use the Settings-owned system-proxy mutation helper")
	}
	statusRead := strings.Index(setup, "const st = await getSystemProxyStatus({throwOnError:true})")
	acknowledge, supportCheck := -1, -1
	if statusRead >= 0 {
		acknowledge = strings.Index(setup[statusRead:], "acknowledged=st")
		supportCheck = strings.Index(setup[statusRead:], "if (!st?.supported)")
	}
	if statusRead < 0 || acknowledge < 0 || supportCheck < 0 || acknowledge > supportCheck {
		t.Error("Setup must retain an unsupported status before returning so a retry can hide the unavailable control")
	}
}
