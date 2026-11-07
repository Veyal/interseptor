package control

import (
	"strings"
	"testing"
)

// Regression: a held request with a binary body reached Repeater with an empty
// body, a stale Content-Length and a toast claiming it was "still held" even
// after Forward + Repeater. The hand-off names the bytes left behind and each
// entry point says what became of the held item.
func TestUIInterceptRepeaterHandoffBinaryAndState(t *testing.T) {
	icpt := readUIAsset(t, "js/intercept.js")
	for _, want := range []string{
		"heldState:'held',binaryBodyLen:heldBinaryLen(sel)",
		"heldState:'forwarded',binaryBodyLen:heldBinaryLen(sel)",
		"heldState:'released',binaryBodyLen:binaryLen",
	} {
		if !strings.Contains(icpt, want) {
			t.Errorf("intercept.js lacks %q", want)
		}
	}
	tools := readUIAsset(t, "js/tools.js")
	for _, want := range []string{"binaryHandoff(raw,binaryBodyLen)", "repeaterLoadedText(heldState)", "if(notice)toast(notice,'warn')"} {
		if !strings.Contains(tools, want) {
			t.Errorf("tools.js lacks %q", want)
		}
	}
	if strings.Contains(tools, "toast('loaded held request into Repeater · it is still held')") {
		t.Error("tools.js must not hard-code the still-held toast")
	}
}

// Regression: text typed under a binary message's head was dropped silently.
func TestUIInterceptBinaryTailWarning(t *testing.T) {
	icpt := readUIAsset(t, "js/intercept.js")
	if !strings.Contains(icpt, "binaryTailWarning($('#heldRaw').value)") || !strings.Contains(icpt, "icpt-body-warn") {
		t.Error("intercept.js must warn while text sits under a binary message's head")
	}
	if !strings.Contains(readUIAsset(t, "panel-intercept-workbench.css"), ".icpt-body-warn{") {
		t.Error("the web intercept sheet must style the binary warning")
	}
}

func TestUIMapQA10Round2Polish(t *testing.T) {
	m := readUIAsset(t, "js/map.js")
	for _, want := range []string{"graphTipHide(); // the hover tooltip", "if(n && !ctxMenuOpen()) graphTipShow(n, ev)", "mapGraphDisplayEps(filtered).length"} {
		if !strings.Contains(m, want) {
			t.Errorf("map.js lacks %q", want)
		}
	}
}
