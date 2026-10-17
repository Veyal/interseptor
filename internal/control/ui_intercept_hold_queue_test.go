package control

import (
	"strings"
	"testing"
)

// Rows added or removed by held/released deltas never pass through
// wireListbox, whose per-row arrow handlers close over the rows of the last
// full render. With one row rendered and forty more arriving as deltas,
// ArrowDown stayed on the first row and End could not reach the last. The hold
// queue therefore handles Arrow/Home/End once, in the capture phase, against
// the rows that are in the list now.
func TestUIHoldQueueKeyboardUsesLiveRows(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"import { wireListbox, setListboxSelection, focusOption } from './listbox.js'",
		"const HELD_NAV_KEYS=['ArrowDown','ArrowUp','Home','End']",
		"const els=[...$('#heldList').querySelectorAll('.icpt-item')]",
		"e.preventDefault();e.stopPropagation();",
		"if(el&&to!==from)selectHeld(Number(el.dataset.id),el.dataset.side);",
		"},true);",
	)
	i := strings.Index(src, "const HELD_NAV_KEYS")
	if i < 0 || !strings.Contains(src[i:i+800], "$('#heldList').addEventListener('keydown'") {
		t.Fatal("hold-queue navigation must be one delegated keydown listener on #heldList")
	}
}

// On phones the hold queue is shown alone and the editor's "Nothing on hold"
// state lives in the hidden detail pane, so an empty queue was a blank area.
// The list now carries the message, shown only in the stacked layout.
func TestUIHoldQueueEmptyStateOnPhones(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src, "<p class=\"icpt-list-empty hint\">Nothing on hold. ${esc(hint?hint.textContent:'')}</p>")
	requireUIContains(t, readUIAsset(t, "panel-scan.css"),
		".icpt-list-empty{display:none;",
		".icpt-work[data-mode=\"stack\"] .icpt-list-empty{display:block}",
	)
}
