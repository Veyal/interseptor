package control

import (
	"regexp"
	"strings"
	"testing"
)

// #104 deferred part — per-item intercept delta SSE. These pin the client wiring
// (the pure reducer logic itself is unit-tested under node in
// ui/_js-tests/intercept-delta.test.mjs, and the server deltas in
// intercept_delta_sse_test.go).

// intercept-delta.js is the pure, node-loadable reducer: no imports, no DOM.
func TestUIInterceptDeltaReducerIsPureAndImportFree(t *testing.T) {
	src := readUIAsset(t, "js/intercept-delta.js")
	if regexp.MustCompile(`(?m)^import `).MatchString(src) {
		t.Error("intercept-delta.js must have no imports so it runs under node")
	}
	exec := executableJS(src)
	for _, forbidden := range []string{"document", "window.", "querySelector", "./core.js"} {
		if strings.Contains(exec, forbidden) {
			t.Errorf("intercept-delta.js must stay DOM-free; found %q", forbidden)
		}
	}
	requireUIContains(t, src,
		"export const SEQ_UNKNOWN = -1",
		"export function applyDelta(intercept, currentSeq, delta)",
		"export function snapshotSeq(snapshot)",
		"status: 'duplicate'",
		"status: 'gap'",
		"status: 'applied'",
	)
	// Contiguity is the whole contract: apply only the exact next seq.
	requireUIContains(t, src, "if (next <= currentSeq)", "if (next !== currentSeq + 1)")
}

// intercept.js applies deltas incrementally (add/remove one row) and rebases on a
// snapshot when it detects a gap — it never trusts a broken sequence.
func TestUIInterceptAppliesDeltasIncrementallyWithGapRecovery(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"import { applyDelta, snapshotSeq, deltaType, SEQ_UNKNOWN } from './intercept-delta.js'",
		"let interceptSeq=SEQ_UNKNOWN",
		"export function applyInterceptDelta(message,resnapshot)",
		"const res=applyDelta(state.intercept||{},interceptSeq,delta)",
		"if(res.status==='duplicate')return",
		"if(res.status==='gap')",
		"interceptSeq=SEQ_UNKNOWN", // stop applying deltas until a snapshot rebases us
		"interceptResnapshotPending=true",
		"state.intercept=res.state",
		"interceptSeq=res.seq",
		// incremental DOM: add one row / remove one row, not a rebuild
		"function addHeldRow(side,item)",
		"function removeHeldRow(side,id)",
		"list.insertBefore(el,list.querySelector('.icpt-item[data-side=\"resp\"]'))",
		"el.remove()",
		// full-render fallbacks for the transitions the incremental path does not own
		"if(heldActionInFlight||forwardAllBusy){renderIntercept();return;}",
		"if(total<=1){renderIntercept();return;}",
		"if(state.heldSel&&state.heldSel.id===id&&state.heldSel.side===side){renderIntercept();return;}",
	)
	// The delta generation is rebased on every authoritative snapshot.
	requireUIContains(t, src, "function noteSnapshotSeq(snapshot)", "if(filterAuthoritative)noteSnapshotSeq(merged)")
	// The shared row markup feeds both the full render and the incremental add.
	requireUIContains(t, src, "function heldRowMarkup(h)", "list.innerHTML=items.map(heldRowMarkup).join('')", "tmp.innerHTML=heldRowMarkup(h)")
}

// app.js routes the snapshot and the two per-item deltas, and feeds the gap
// recovery its snapshot refetch (refreshIntercept).
func TestUIAppRoutesInterceptDeltaSSE(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"applyInterceptDelta",
		"m.type==='intercept.state'",
		"m.type==='intercept.held'||m.type==='intercept.released'",
		"applyInterceptDelta(m,refreshIntercept)",
	)
	// The original full-snapshot handler stays (reset path + contract).
	requireUIContains(t, app, "m.type==='intercept.update'", "replaceInterceptState(m.intercept)")
}
