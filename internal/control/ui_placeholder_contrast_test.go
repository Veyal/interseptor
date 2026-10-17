package control

import (
	"regexp"
	"testing"
)

// Placeholders often carry the only format hint ("host e.g. *.example.com"), so
// they use the tuned --fg3 token at full opacity instead of the browser default
// grey (3.1:1 on dark). Small muted labels must not stack opacity on --fg3 either:
// the token contrast checks are only meaningful when nothing fades the text later.
func TestUIFoundationPlaceholderAndMutedTextContrast(t *testing.T) {
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css, "::placeholder{color:var(--fg3);opacity:1}")
	for _, re := range []string{
		`::placeholder\{[^}]*opacity:\.`,
		`\.nav-rail-group-label\{[^}]*opacity:`,
		`#repSend \.kbd\{[^}]*opacity:`,
	} {
		if regexp.MustCompile(re).MatchString(css) {
			t.Errorf("app.css fades small text below AA: %s", re)
		}
	}
	for _, theme := range []struct{ name, prefix string }{
		{"dark", ":root{"}, {"light", `:root[data-theme="light"]{`}, {"hc", `:root[data-theme="hc"]{`},
	} {
		vars := foundationTheme(t, css, theme.prefix)
		page := resolve(t, vars, vars["--bg"], rgb{255, 255, 255}, 0)
		for _, s := range []string{"--bg", "--bg1", "--bg2", "--bg3"} {
			bg := resolve(t, vars, vars[s], page, 0)
			if got := contrastRatio(resolve(t, vars, vars["--fg3"], bg, 0), bg); got < 4.5 {
				t.Errorf("%s theme: placeholder --fg3 on %s = %.2f:1, want >= 4.5:1", theme.name, s, got)
			}
		}
	}
}
