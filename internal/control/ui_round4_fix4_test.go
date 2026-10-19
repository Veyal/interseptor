package control

import (
	"strings"
	"testing"
)

// Repeater "+" must not be listed in the transparent icon-button :is() (its
// #themeToggle member gives it ID specificity that beats the opaque rule).
func TestDesktopRepeaterTabAddStaysOpaque(t *testing.T) {
	css := readUIAsset(t, "css/desktop/components.css")
	if strings.Contains(css, "#themeToggle,.tr-more,.rep-tab-add") {
		t.Error(".rep-tab-add must not share the transparent :is(.pane-btn,#themeToggle,...) rule")
	}
	if !strings.Contains(css, "html.desktop-app .rep-tabs .rep-tab-add{background-color:var(--surface-0)}") {
		t.Error("opaque .rep-tab-add rule is missing")
	}
}

// Tab edits typed inside the persist debounce must be flushed on close, and
// the Intruder payload editor must never flip read-only mid-typing.
func TestTabPersistenceFlushesAndIntruderTypingIsNeverDropped(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	for _, want := range []string{"mgr.flushPending=", "flushOnClose({isDirty:()=>mgr.persistPending"} {
		if !strings.Contains(core, want) {
			t.Errorf("js/core.js tab manager is missing %q", want)
		}
	}
	tools := readUIAsset(t, "js/tools.js")
	for _, want := range []string{"label:repTitle(o)", "const collapseLarge=", "ta.addEventListener('blur'", "insertFrom(Paste|Drop)", "Esc closes the run history"} {
		if !strings.Contains(tools, want) {
			t.Errorf("js/tools.js is missing %q", want)
		}
	}
	if strings.Contains(tools, "'tab '+o.tid") {
		t.Error("Diff against tab must label tabs with repTitle like the strip")
	}
}

func TestToastDeduplicatesIdenticalLiveNotices(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, "!el.classList.contains('dying')) return el;") {
		t.Error("toast() must reuse an identical notice that is still on screen")
	}
}

func TestDesktopIntruderHistoryIsADrawerBelowWideWindows(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-intruder.css")
	if !strings.Contains(css, "@media (min-width:900px) and (max-width:1439px)") || !strings.Contains(css, "#intrHistory{position:absolute") {
		t.Error("Intruder run history must overlay the setup column below 1440px")
	}
	rep := readUIAsset(t, "css/desktop/panel-repeater.css")
	if !strings.Contains(rep, "@container dsk-rep-res (max-width:300px)") {
		t.Error("Repeater response header must stay on one line in a narrow pane")
	}
}
