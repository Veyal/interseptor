package control

import (
	"strings"
	"testing"
)

// Intercept has two independent mutation classes: filter configuration does
// not emit an SSE update, while safety toggles may drain a queue and do emit
// one. Keep these contracts source-level so a future refactor cannot make a
// stalled filter block a safety action or let an old summary erase held items.
func TestUIInterceptFilterSSEMergeKeepsLocalMutationAuthoritative(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, intercept,
		"let pendingFilterMutation=null",
		"let filterMutationEpoch=0",
		"function stageInterceptFilter()",
		"pendingFilterMutation={epoch,config,input}",
		"function mergeIncomingInterceptState(next)",
		"pendingFilterMutation.config",
		"replaceInterceptState(next)",
	)
	if !strings.Contains(intercept, "const merged=mergeIncomingInterceptState(next)") {
		t.Fatal("incoming intercept summaries must merge a pending local filter before replacing state")
	}
	if strings.Contains(intercept, "state.intercept=next;interceptStateEpoch++") {
		t.Fatal("replaceInterceptState must not blindly replace a summary while a filter mutation is pending")
	}
}

func TestUIInterceptSafetyTogglesAreNotQueuedBehindFilter(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, intercept,
		"let interceptMutationTail=Promise.resolve()",
		"let interceptFilterMutationTail=Promise.resolve()",
		"let interceptSummaryEpoch=0",
		"async function applyFilterMutation(request,draft)",
		"interceptFilterMutationTail.then(async()=>",
		"const generation=interceptSummaryEpoch",
		"const summary=await request()",
		"const summaryCurrent=generation===interceptSummaryEpoch",
		"replaceInterceptState(summary)",
		"await applyFilterMutation(",
		"await applyInterceptMutation(",
	)
	if strings.Count(intercept, "await applyFilterMutation(") != 1 {
		t.Errorf("filter changes should use exactly one independent mutation lane, got %d", strings.Count(intercept, "await applyFilterMutation("))
	}
}

func TestUIInterceptFailedFilterRestoresAcknowledgedControls(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, intercept,
		"let acknowledgedFilterConfig=null",
		"function filterControlsMatch(input)",
		"function syncFilterControls(config)",
		"acknowledgedFilterConfig={...config}",
		"else if(latest)commitFilterConfig(config)",
		"if(filterControlsMatch(input))syncFilterControls(fallback)",
	)
}

func TestUIInterceptDraftOwnsFilterFieldsBeforeDebounce(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, intercept,
		"const draft=stageInterceptFilter()",
		"icptFilterTimer=setTimeout(()=>applyInterceptFilter(draft),650)",
		"if(epoch!==filterMutationEpoch)return false",
	)
}
