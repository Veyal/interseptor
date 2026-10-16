package control

import (
	"strings"
	"testing"
)

// Review fixes: a closing sheet must finish before the same id reopens, and the
// dock and sheets share one soft-keyboard watcher.

func TestUISheetReopenFinishesPendingCloseFirst(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/sheet.js"))
	requireUIContains(t, src,
		"entry.pendingFinish = once",
		"if (entry && !entry.open && entry.pendingFinish) entry.pendingFinish();",
		"if (entry.open) return; // reopened meanwhile",
		"window.clearTimeout(timer)",
		"removeEventListener('transitionend', once)",
	)
	// the flush has to run before the entry is reused and marked open again
	flush := strings.Index(src, "entry.pendingFinish();")
	reopen := strings.Index(src, "entry.open = true;")
	if flush < 0 || reopen < 0 || flush > reopen {
		t.Error("openSheet must flush a pending close before it marks the sheet open")
	}
}

func TestUISoftKeyboardWatcherIsShared(t *testing.T) {
	shared := executableJS(readUIAsset(t, "js/soft-keyboard.js"))
	requireUIContains(t, shared, "export function watchSoftKeyboard()", "shouldHideDock(", "visualViewport")
	for _, name := range []string{"js/sheet.js", "js/dock.js"} {
		src := executableJS(readUIAsset(t, name))
		requireUIContains(t, src, "import { watchSoftKeyboard } from './soft-keyboard.js'")
		if strings.Contains(src, "visualViewport") || strings.Contains(src, "dataset.softKeyboard") {
			t.Errorf("%s must use the shared watchSoftKeyboard instead of its own visualViewport listener", name)
		}
	}
}
