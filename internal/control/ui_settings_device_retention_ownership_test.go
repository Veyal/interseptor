package control

import (
	"strings"
	"testing"
)

func TestUIDeviceDiscoveryUsesIndependentLoadOwnership(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"let androidLoadEpoch=0",
		"let androidActionPending=false",
		"const epoch=++androidLoadEpoch",
		"if(epoch!==androidLoadEpoch||(androidActionPending&&!allowDuringAction))return null",
		"let iosLoadEpoch=0",
		"let iosActionPending=false",
		"let iosSshLoadEpoch=0",
		"let iosSshActionPending=false",
		"if(epoch!==iosLoadEpoch||(iosActionPending&&!allowDuringAction))return null",
		"if(epoch!==iosSshLoadEpoch||(iosSshActionPending&&!allowDuringAction))return null",
	)
	for _, marker := range []string{
		"androidActionPending=true", "androidActionPending=false",
		"iosActionPending=true", "iosActionPending=false",
		"iosSshActionPending=true", "iosSshActionPending=false",
		"setAndroidActionBusy(true)", "setAndroidActionBusy(false)",
		"setIOSActionBusy(true)", "setIOSActionBusy(false)",
		"setIOSSshActionBusy(true)", "setIOSSshActionBusy(false)",
		"await loadAndroid({allowDuringAction:true})",
		"await loadIOS({allowDuringAction:true})",
		"await loadIOSSsh({allowDuringAction:true})",
		"await mobileReadiness('#androidAdbHint')",
		"await mobileReadiness('#iosHint')",
		"await mobileReadiness('#iosSshHint')",
		"let iosSshRestored=false",
		"let iosSshDirty=false",
		"if(iosSshRestored||iosSshDirty)return",
		"trigger.disabled=true",
	} {
		if !strings.Contains(settings, marker) {
			t.Errorf("device action ownership marker %q missing", marker)
		}
	}
}

func TestUIRetentionRefreshAndDestructiveActionsHaveOwnership(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"let retentionLoadEpoch=0",
		"let retentionPolicyLoadEpoch=0",
		"function snapshotRetentionSelection()",
		"function restoreRetentionSelection(selection)",
		"const epoch=++retentionLoadEpoch",
		"const epoch=++retentionPolicyLoadEpoch",
		"let retentionMutationPromise=null",
		"function runRetentionMutation(action)",
		"const previous=retentionMutationPromise||Promise.resolve()",
		"const current=previous.catch(()=>{}).then(action)",
		"retentionSelectionSnapshot=selection||new Set()",
		"if(b.dataset.sec==='project'){retentionLoaded=true;loadRetention();}",
	)
	if strings.Contains(settings, "loadRetentionPolicy();\n  try{const d=await api('/api/hosts/stats')") {
		t.Error("retention stats and policy loads should not be left as unowned concurrent refreshes")
	}
}

func TestUIProjectModalDoesNotFocusAfterStaleAsyncLoad(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"if(epoch!==projectModalLoadEpoch)return false",
		"const rendered=await renderProjModal()",
		"if(rendered&&inp&&m.style.display==='flex'&&!inp.disabled)inp.focus();",
	)
	if !strings.Contains(settings, "rendered=await renderProjModal()") {
		t.Error("project modal must gate focus on the current async render")
	}
}
