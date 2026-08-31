package control

import (
	"strings"
	"testing"
)

// The flow picker has two independent async ownership boundaries: its modal
// owner and its current search query. Keep these source contracts close to the
// embedded module so a future refactor cannot reintroduce cross-finding or
// stale-search writes without a test failure.
func TestUIFindingsFlowPickerGuardsAsyncOwnership(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	for _, want := range []string{
		"let flowPickEpoch=0",
		"let flowPickSearchEpoch=0",
		"const searchEpoch=++flowPickSearchEpoch",
		"const ownerEpoch=flowPickEpoch",
		"const ownerFindingId=flowPickFindingId",
		"const initialSearchEpoch=++flowPickSearchEpoch",
		"if(initialSearchEpoch===flowPickSearchEpoch)flowPickFlows=incoming",
		"renderFlowPickList(search?.value||'')",
		"if(ownerEpoch!==flowPickEpoch||ownerFindingId!==flowPickFindingId",
		"if(epoch!==flowPickEpoch||flowPickFindingId!==findingId",
		"$('#findFlowPickModal')?.style.display!=='none'",
		"flowPickEpoch++",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("flow picker async ownership contract missing %q", want)
		}
	}
	if !strings.Contains(findings, "if(searchEpoch!==flowPickSearchEpoch)") {
		t.Error("older flow-picker searches must not render over the latest query")
	}
	if !strings.Contains(findings, "if(!flowPickSearchCurrent(q))return") {
		t.Error("flow-picker search completions must verify the current input value")
	}
}

func TestUIFindingsCreateModalOwnsAndRestoresKeyboardFocus(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	for _, want := range []string{
		"function openFindCreate(event)",
		"const trigger=event?.currentTarget",
		"trigger.focus({preventScroll:true})",
		"openModal($('#findCreateModal'),{initialFocus:$('#fcTitle'),",
		"onEscape:closeFindingCreate",
		"onDismiss:closeFindingCreate",
		"findingCreateFocus",
		"$('#fcStatus')",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("finding create focus ownership contract missing %q", want)
		}
	}
	if strings.Contains(findings, "openModal($('#findCreateModal'));\n  $('#fcTitle').focus()") {
		t.Error("finding create must not race the modal manager with direct focus")
	}
}
