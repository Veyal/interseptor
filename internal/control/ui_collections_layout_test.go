package control

import (
	"io/fs"
	"strings"
	"testing"
)

// Layout regressions found in the browser pass over the Collections panel. The
// UI has no build step, so these are static assertions against the embedded
// assets (the screenshots live in the release evidence, not here).
func TestCollectionsSheetsKeepTheirLayoutGuards(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(uiFS, "ui/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	css, matrixCSS := read("css/collections.css"), read("css/collections-matrix.css")
	sheets, env := read("js/collections-sheets.js"), read("js/collections-env.js")

	for _, want := range []string{
		".coll-kv-vars{min-width:",      // variables never squeeze to a few pixels
		"@container (max-width: 34rem)", // narrow sheet: one card per variable
		"td[data-label]::before",        // cards label the initial and current value
		"bottom:calc(-1 * var(--sp-4))", // sticky footer covers the sheet's bottom padding
	} {
		if !strings.Contains(css, want) {
			t.Errorf("collections.css lost %q", want)
		}
	}
	if !strings.Contains(sheets, "coll-kv coll-kv-vars") || !strings.Contains(sheets, "coll-vars-wrap") {
		t.Error("the variables table must use the guarded classes")
	}
	if !strings.Contains(matrixCSS, ".cxm-table td.cxm-row-name{") {
		t.Error("the matrix row name rule must out-rank `.cxm-table td{white-space:nowrap}` so long names wrap")
	}
	// The native file control is replaced by the app button (custom-ui-controls).
	if !strings.Contains(sheets, "file.hidden = true") || !strings.Contains(sheets, "'Choose files'") {
		t.Error("the import sheet must not expose the native file input")
	}
	// On phones the scripts chip keeps only the count, so it needs an accessible name.
	if !strings.Contains(env, "chip.setAttribute('aria-label'") || !strings.Contains(env, "long.className = 'lbl-long'") {
		t.Error("the scripts chip must shorten on phones and keep its accessible name")
	}
}
