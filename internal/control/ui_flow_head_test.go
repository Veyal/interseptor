package control

import (
	"regexp"
	"strings"
	"testing"
)

// TestUIFlowHeadTemplateUsesOnlyDeclaredIdentifiers guards the v2.4.1 boot
// crash: renderFlowHead kept a "${align}" placeholder after the identifier was
// removed, throwing ReferenceError on load and aborting the whole workspace.
func TestUIFlowHeadTemplateUsesOnlyDeclaredIdentifiers(t *testing.T) {
	src := readUIAsset(t, "js/proxy.js")
	start := strings.Index(src, "export function renderFlowHead(){")
	if start < 0 {
		t.Fatal("renderFlowHead not found in js/proxy.js")
	}
	body := src[start:]
	end := strings.Index(body, ".join('')")
	if end < 0 {
		t.Fatal("renderFlowHead header template join not found")
	}
	body = body[:end]

	declared := map[string]bool{"k": true, "c": true, "x": true}
	for _, m := range regexp.MustCompile(`\bconst\s+(\w+)\s*=`).FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\$\{\s*([A-Za-z_$][\w$]*)\s*\}`).FindAllStringSubmatch(body, -1) {
		if !declared[m[1]] {
			t.Errorf("renderFlowHead template interpolates undeclared identifier %q (ReferenceError at boot)", m[1])
		}
	}
	if !strings.Contains(body, "${title}") {
		t.Error("renderFlowHead should still interpolate the declared title attribute")
	}
}
