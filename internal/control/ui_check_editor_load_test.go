package control

import (
	"strings"
	"testing"
)

func TestUICheckEditorLoadOwnsSelectionAndActionLifecycle(t *testing.T) {
	src := readUIAsset(t, "js/scanner.js")

	requireUIContains(t, src,
		"let checkEditorReady=false",
		"let checkEditorLoading=false",
		"let checkRestoreFocus=false",
		"function setCheckEditorLoadState",
		"function setCheckOutcome",
		"kind==='error'?'alert':'status'",
		"kind==='error'?'assertive':'polite'",
		"data-check-retry",
		"event.detail===0",
		"target.focus({preventScroll:true})",
		"button.focus({preventScroll:true})",
		"checkEditorLoading||!checkEditorReady",
	)
	if got := strings.Count(src, "out.innerHTML="); got != 1 {
		t.Errorf("check outcomes should be written through setCheckOutcome, found %d direct writes", got)
	}

	for _, name := range []string{"loadBuiltinCheck", "loadCheck"} {
		anchor := "export async function " + name
		if name == "loadCheck" {
			anchor += "(id"
		}
		start := strings.Index(src, anchor)
		if start < 0 {
			t.Fatalf("scanner.js is missing %s", name)
		}
		end := strings.Index(src[start+1:], "export ")
		if end < 0 {
			end = len(src) - start - 1
		}
		segment := src[start : start+1+end]
		requireUIContains(t, segment,
			"setCheckEditorLoadState('loading'",
			"$('#checkSrc').value=''",
			"setCheckEditorLoadState('ready'",
			"setCheckEditorLoadState('error'",
		)
	}

	for _, name := range []string{"checkTest", "checkSave", "checkDelete"} {
		start := strings.Index(src, "export async function "+name)
		if start < 0 {
			t.Fatalf("scanner.js is missing %s", name)
		}
		end := strings.Index(src[start+1:], "export ")
		if end < 0 {
			end = len(src) - start - 1
		}
		segment := src[start : start+1+end]
		if !strings.Contains(segment, "checkEditorLoading||!checkEditorReady") {
			t.Errorf("%s does not guard actions until the selected check is loaded", name)
		}
	}
}
