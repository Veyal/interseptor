package control

import (
	"strings"
	"testing"
)

// Scanner has several overlapping async surfaces. These source contracts keep
// request ownership explicit in the embedded, build-free UI.
func TestUIScannerClearInvalidatesPriorResults(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"scanRunPending=false,scanClearEpoch=0,scanClearPending=false,scanResultsRefreshPending=false",
		"if(scanRunPending||scanClearPending)",
		"if(scanRunPending||scanClearPending){scanResultsRefreshPending=true;return;}",
		"clear.disabled=scanRunPending||scanClearPending",
		"const clearResultsEpoch=++scanResultsEpoch",
		"const clearRunEpoch=++scanRunEpoch",
		"clearResultsEpoch!==scanResultsEpoch",
		"clearRunEpoch!==scanRunEpoch",
		"setScanRunState('idle','Run scan ▸')",
		"if(scanResultsRefreshPending){scanResultsRefreshPending=false;loadIssues();}",
		"if(!confirmed)return",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scanner clear ownership contract missing %q", want)
		}
	}
}

func TestUIScannerTargetPrefillPreservesNewerIntent(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let scanTargetLoadEpoch=0",
		"const epoch=++scanTargetLoadEpoch",
		"let scannerPrefillEpoch=0",
		"const prefillEpoch=++scannerPrefillEpoch",
		"if(epoch!==scanTargetLoadEpoch)return",
		"if(prefillEpoch!==scannerPrefillEpoch)return",
		"scanTargetLoadEpoch++",
		"f.value=pathSearch||''",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scanner target/prefill ownership contract missing %q", want)
		}
	}
}

func TestUIScannerCheckPacksOwnLoadsAndMutations(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let checkPacksLoadEpoch=0",
		"let checkPackMutationEpoch=0",
		"let checkPackMutationBusy=false",
		"const epoch=++checkPacksLoadEpoch",
		"if(epoch!==checkPacksLoadEpoch||checkPackMutationBusy)return",
		"const mutationEpoch=++checkPackMutationEpoch",
		"if(mutationEpoch!==checkPackMutationEpoch)return",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scanner check-pack ownership contract missing %q", want)
		}
	}
}

func TestUIScannerCheckActionsSnapshotDraftOwnership(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let checkDraftEpoch=0",
		"const draftEpoch=checkDraftEpoch",
		"const source=$('#checkSrc').value",
		"const id=$('#checkId').value.trim()",
		"if(draftEpoch!==checkDraftEpoch)return",
		"if((state.selId||0)!==flowId)",
		"if(checkActionBusy)return",
		"function closeChecks()",
		"openModal($('#checksModal'),{onEscape:closeChecks,onDismiss:closeChecks})",
		"setCheckActionState('delete','pending')",
		"checkLoadEpoch++;checkDraftEpoch++;cancelCheckAction()",
		"loadBuiltinCheck(id,{preserveAction:true})",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scanner check draft ownership contract missing %q", want)
		}
	}
}

func TestUIOOBBaseLoadPreservesBlurredDrafts(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let oobBaseEditEpoch=0",
		"let oobBaseAcknowledgedEditEpoch=0",
		"let oobBaseSaveEpoch=0",
		"let oobBaseSaveQueue=Promise.resolve()",
		"const submitted={editEpoch:oobBaseEditEpoch,value:input.value.trim()}",
		"const baseLoadEditEpoch=oobBaseEditEpoch",
		"baseOwner.editEpoch===oobBaseEditEpoch&&base.value.trim()===baseOwner.value",
		"baseLoadEditEpoch===oobBaseEditEpoch&&oobBaseEditEpoch===oobBaseAcknowledgedEditEpoch",
		"oobBaseSaveQueue.catch(()=>{}).then(()=>api('/api/oob/base'",
		"oobBaseSaveQueue=task.catch(()=>{})",
		"oobBaseAcknowledgedEditEpoch=submitted.editEpoch",
		"if(saveEpoch!==oobBaseSaveEpoch)return",
		"await loadOob(submitted)",
		"addEventListener('input',()=>{oobBaseEditEpoch++;})",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("OOB base draft ownership contract missing %q", want)
		}
	}
}
