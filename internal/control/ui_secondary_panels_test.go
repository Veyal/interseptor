package control

import (
	"regexp"
	"strings"
	"testing"
)

// Secondary panels (API keys, Authz, Setup, TLS diagnosis) use the shared
// utility classes instead of per-instance inline styles; the only inline style
// left is a data-driven colour (`style="color:${...}"`).
func TestUISecondaryPanelsAvoidStaticInlineStyles(t *testing.T) {
	static := regexp.MustCompile(`style="[^"$]*"`)
	for _, name := range []string{"js/apipanel.js", "js/authz.js", "js/setup.js", "js/tlsdiag.js"} {
		if m := static.FindAllString(readUIAsset(t, name), -1); len(m) != 0 {
			t.Errorf("%s keeps static inline styles: %v", name, m)
		}
	}
	for _, name := range []string{"js/tlsdiag.js", "js/settings.js"} {
		if strings.Contains(readUIAsset(t, name), "style.cssText") && name == "js/tlsdiag.js" {
			t.Errorf("%s still writes style.cssText", name)
		}
	}
	if strings.Contains(readUIAsset(t, "js/settings.js"), "row.style.cssText") {
		t.Error("settings.js still builds rows with style.cssText")
	}
}

func TestUISecondaryPanelsUseToastErrorAndDefinedClasses(t *testing.T) {
	for _, name := range []string{"js/apipanel.js", "js/authz.js", "js/setup.js", "js/tlsdiag.js"} {
		src := readUIAsset(t, name)
		if regexp.MustCompile(`toast\(e\.message|toast\('[^']*'\s*\+\s*e\.message`).MatchString(src) {
			t.Errorf("%s reports failures with raw toast(e.message); use toastError", name)
		}
	}
	settings := readUIAsset(t, "js/settings.js")
	for _, bad := range []string{"toast('purge: '+e.message)", "toast('gc: '+e.message)"} {
		if strings.Contains(settings, bad) {
			t.Errorf("settings.js still uses %s", bad)
		}
	}
	if !strings.Contains(settings, `aria-label="Select host `) {
		t.Error("retention row checkboxes need an accessible 'Select host' name")
	}
	css := readUIAsset(t, "surfaces.css") + readUIAsset(t, "app.css")
	classRe := regexp.MustCompile(`class="([^"]*)"`)
	for _, name := range []string{"js/apipanel.js", "js/authz.js", "js/setup.js", "js/tlsdiag.js"} {
		for _, m := range classRe.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			for _, c := range strings.Fields(m[1]) {
				if (strings.HasPrefix(c, "u-") || strings.HasPrefix(c, "matrix-") || c == "cell-note" || c == "pre-scroll" || c == "panel-details") && !strings.Contains(css, "."+c) {
					t.Errorf("%s uses undefined utility class %q", name, c)
				}
			}
		}
	}
}
