package control

import (
	"regexp"
	"strings"
	"testing"
)

// findings.css and surfaces.css must follow the same token contract as app.css:
// every var() resolves, no literal type sizes, and no off-scale radii.
func TestUISecondaryStylesheetsUseDeclaredTokens(t *testing.T) {
	app := readUIAsset(t, "app.css")
	declared := parseThemeBlock(t, app, ":root{")
	for _, name := range []string{"findings.css", "surfaces.css"} {
		css := readUIAsset(t, name)
		for _, m := range regexp.MustCompile(`var\((--[a-zA-Z0-9-]+)([,)])`).FindAllStringSubmatch(css, -1) {
			if _, ok := declared[m[1]]; !ok && !strings.Contains(css, m[1]+":") {
				t.Errorf("%s reads undeclared token %s", name, m[1])
			}
		}
		for _, m := range regexp.MustCompile(`font-size:\s*([0-9.]+)px`).FindAllStringSubmatch(css, -1) {
			t.Errorf("%s: literal font-size:%spx bypasses the --fs-* scale", name, m[1])
		}
		for _, m := range regexp.MustCompile(`font:[^;}]*?\b([0-9.]+)px`).FindAllStringSubmatch(css, -1) {
			t.Errorf("%s: literal %spx in font shorthand bypasses the --fs-* scale", name, m[1])
		}
		for _, m := range regexp.MustCompile(`border-radius:\s*([^;}]*)`).FindAllStringSubmatch(css, -1) {
			if regexp.MustCompile(`\b(4|7|8|10)px`).MatchString(m[1]) {
				t.Errorf("%s: off-scale radius %q; use --r-* tokens", name, m[1])
			}
		}
	}
	if _, ok := declared["--fs-3xl"]; !ok {
		t.Error("--fs-3xl token is missing")
	}
}

func TestUIAppStylesheetRadiiAreOnScale(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, m := range regexp.MustCompile(`border-radius:\s*([^;}]*)`).FindAllStringSubmatch(css, -1) {
		if regexp.MustCompile(`\b(4|7|8|10)px`).MatchString(m[1]) {
			t.Errorf("app.css: off-scale radius %q; use --r-* tokens", m[1])
		}
	}
}
