package control

import (
	"regexp"
	"strings"
	"testing"
)

// History can copy a flow as a PNG through the shared copyImageButton primitive.
func TestHistoryFlowCopyAsPNGWiring(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	drawer := readUIAsset(t, "js/flowdrawer.js")
	core := readUIAsset(t, "js/core.js")
	image := readUIAsset(t, "js/copy-image.js")
	html := readUIAsset(t, "index.html")

	if !strings.Contains(proxy, "mountFlowCopyPng(") {
		t.Fatal("proxy.js must render the inspector Copy as PNG control")
	}
	if !strings.Contains(drawer, "mountFlowCopyPng(") {
		t.Fatal("flowdrawer.js must render the drawer Copy as PNG control")
	}
	for _, src := range []string{"#inspectCopyPng", "#fdCopyPng"} {
		if !strings.Contains(proxy+drawer, src) {
			t.Fatalf("missing mount point %s", src)
		}
		if !strings.Contains(html, `id="`+strings.TrimPrefix(src, "#")+`"`) {
			t.Fatalf("index.html missing %s", src)
		}
	}
	// One wrapper builds the URL and delegates to the shared primitive.
	if !strings.Contains(core, "export function flowCopyPngButton(") || !strings.Contains(core, "copyImageButton(flowPreviewPngURL(") {
		t.Fatal("flowCopyPngButton must call copyImageButton(flowPreviewPngURL(...))")
	}
	if !regexp.MustCompile("/api/flows/\\$\\{[^}]+\\}/preview\\.png").MatchString(image) {
		t.Fatal("flowPreviewPngURL must build /api/flows/${id}/preview.png")
	}
	for _, want := range []string{"['both', 'req', 'res'], 'both'", "['vertical', 'horizontal'], 'vertical'", "? 0 : 1"} {
		if !strings.Contains(image, want) {
			t.Fatalf("flowPreviewPngURL missing default %s", want)
		}
	}
	// Accessible name identifies the flow.
	if !strings.Contains(core, "'Copy flow #' + id + ' request and response as PNG'") {
		t.Fatal("control aria-label must read: Copy flow #N request and response as PNG")
	}
	// Theme follows the active UI theme at click time.
	if !strings.Contains(core, "data-copy-theme") || !strings.Contains(image, "export function activeFlowTheme(") {
		t.Fatal("copy control must follow the active theme")
	}
}

func TestInsecureContextCopyMessageExplainsCauseAndRemedy(t *testing.T) {
	image := readUIAsset(t, "js/copy-image.js")
	const want = "Copying images needs HTTPS or localhost: use the download button, or serve the UI over HTTPS."
	if !strings.Contains(image, want) {
		t.Fatalf("insecure message missing: %s", want)
	}
	if len(want) > 120 {
		t.Fatalf("message too long: %d", len(want))
	}
	if strings.Contains(image, "Clipboard image copy needs https or localhost") {
		t.Fatal("old vague insecure message still present")
	}
}
