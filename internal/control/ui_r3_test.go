package control

import (
	"regexp"
	"strings"
	"testing"
)

// The Map table at phone width shows Method, Path, Status and the row action without
// sideways scrolling: Host and Hits are folded into the remaining cells by CSS, so the
// column widths must live on classes (an inline width cannot be overridden by a media query).
func TestUIR3MapTableFoldsColumnsOnPhones(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/map.js"))
	for _, want := range []string{
		`class="map-tbl-host"`,
		`<span class="map-tbl-hostfold">${esc(e.host)}</span>`,
		`<span class="map-tbl-hitsfold">`,
		`class="map-tbl-hits"`,
		"class=\"map-th-${k}${sk === k ? ' sorted' : ''}\"",
		"' map-tbl-multihost'",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("js/map.js: missing %q", want)
		}
	}
	if regexp.MustCompile(`th\('[a-z]+', '[A-Za-z]+', '\d+px'\)`).MatchString(js) {
		t.Error("js/map.js: a sortable header still carries an inline width; the phone layout cannot fold it")
	}
	css := readUIAsset(t, "panel-misc.css")
	for _, want := range []string{
		".map-th-host{width:140px}", ".map-th-method{width:72px}", ".map-th-status{width:88px}", ".map-th-hits{width:52px}",
		".map-tbl-hostfold,.map-tbl-hitsfold{display:none}",
		".map-th-host,.map-tbl-host,.map-th-hits,.map-tbl-hits{display:none}",
		".map-tbl,.map-virt-scroll>table.map-tbl{table-layout:fixed;min-width:0}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-misc.css: missing %q", want)
		}
	}
	// Desktop keeps its table: the folds are phone-only.
	for _, rule := range []string{".map-th-host,.map-tbl-host,.map-th-hits,.map-tbl-hits{display:none}", ".map-tbl,.map-virt-scroll>table.map-tbl{"} {
		i := strings.Index(css, rule)
		if i < 0 || strings.LastIndex(css[:i], "@media (max-width:720px){") < strings.LastIndex(css[:i], "\n}\n") {
			t.Errorf("%q must live inside @media (max-width:720px)", rule)
		}
	}
	if !strings.Contains(readUIAsset(t, "app.css"), ".map-virt-scroll>table{table-layout:fixed;min-width:640px}") {
		t.Error("app.css: the desktop windowed table keeps its 640px floor")
	}
}

// The Intruder strip is role-less (the tablist is #intrTabsList, created by the tab
// manager), so the run lock's busy state belongs on the tablist.
func TestUIR3IntruderRunLockMarksTheTablistBusy(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/tools.js"))
	body := js[strings.Index(js, "function syncIntrTabLock(locked)"):]
	body = body[:strings.Index(body, "function activeIntrHistory()")]
	if strings.Contains(body, "bar.setAttribute('aria-busy'") {
		t.Error("syncIntrTabLock writes aria-busy on the role-less strip")
	}
	for _, want := range []string{`bar.querySelector('[role="tablist"]')`, "list.setAttribute('aria-busy',locked?'true':'false')", "bar.removeAttribute('aria-busy')"} {
		if !strings.Contains(body, want) {
			t.Errorf("syncIntrTabLock: missing %q", want)
		}
	}
}

// A phone tag chip is an atomic inline: as a positioned plain inline it was painted over
// the path cell's ellipsis whenever the tags overflowed the cell.
func TestUIR3PhoneTagChipsDoNotCoverTheEllipsis(t *testing.T) {
	css := readUIAsset(t, "panel-proxy.css")
	rule := "#panel-proxy #rows .trow .flowtag{position:relative;display:inline-block;line-height:13px}"
	i := strings.Index(css, rule)
	if i < 0 {
		t.Fatalf("panel-proxy.css: missing %q", rule)
	}
	if strings.LastIndex(css[:i], "@media (max-width:720px){") < strings.LastIndex(css[:i], "\n}\n") {
		t.Error("the chip rule must stay phone-only")
	}
}
