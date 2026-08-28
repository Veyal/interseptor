package control

import (
	"strings"
	"testing"
)

func TestUIFindingsSerializesAndCoalescesMutationWrites(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"findingWriteQueues",
		"pendingFields",
		"pendingWaiters",
		"latestValues",
		"function pendingFindingValue(id, key, fallback)",
		"function acknowledgedFindingValue(id, key, fallback)",
		"drainFindingWrites",
		"Object.assign(queue.pendingFields, fields)",
		"queue.latestValues[key] = fields[key]",
		"const waiters = queue.pendingWaiters.splice(0)",
		"await api('/api/findings/' + id",
		"const expected = pendingFindingValue(f.id, key, previous)",
		"if (v === expected) return",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings writes must serialize/coalesce latest intent: missing %q", want)
		}
	}
}

func TestUIFindingsRevertDuringWriteUsesCurrentAcknowledgedFallback(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"const authoritative = acknowledgedFindingValue(f.id, key, previous)",
		"if (el.value === v) el.value = authoritative",
		"acknowledgedFindingValue(f.id, 'status', previous)",
		"acknowledgedFindingValue(f.id, 'severity', previous)",
		"acknowledgedFindingValue(f.id, 'environment', previous)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("failed latest edit must roll back to the value acknowledged by then: missing %q", want)
		}
	}
}

func TestUIFindingsDeferredRefreshRestoresStableDetailFocus(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"captureFindingFocus",
		"restoreFindingFocus",
		"active.tabIndex",
		"const focus = captureFindingFocus()",
		"restoreFindingFocus(focus)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("deferred finding refresh must preserve stable detail focus: missing %q", want)
		}
	}
}

func TestUIFindingsDebouncesBodiesPerFinding(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"let bodySaveTimers = new Map()",
		"const previous = bodySaveTimers.get(fid)",
		"bodySaveTimers.set(fid",
		"bodySaveTimers.delete(fid)",
		"bodySaveTimers.has(selFinding)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("finding body debounce must be entity-scoped: missing %q", want)
		}
	}
	if strings.Contains(findings, "clearTimeout(bodySaveTimer)") {
		t.Error("one finding must not cancel another finding's pending body save")
	}
}
