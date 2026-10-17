package control

import (
	"regexp"
	"testing"
)

// The Report preflight's icon helper pointed at "#alert" instead of the
// sprite's "#i-alert", so every blocker, reason and empty-state icon rendered
// as blank space. Each icon it names must resolve in the sprite.
// The evidence tray's helper had the same fault (its move and remove buttons).
func TestUIReportPreflightIconsResolveInSprite(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, file := range []string{"js/report-preflight.js", "js/evidence-tray.js"} {
		js := executableJS(readUIAsset(t, file))
		requireUIContains(t, js, `<use href="#i-${name}"/>`)
		names := regexp.MustCompile(`icon\('([a-z0-9-]+)'\)`).FindAllStringSubmatch(js, -1)
		if len(names) == 0 {
			t.Fatalf("%s: no icon() calls found", file)
		}
		for _, m := range names {
			requireUIContains(t, index, `id="i-`+m[1]+`"`)
		}
	}
}
