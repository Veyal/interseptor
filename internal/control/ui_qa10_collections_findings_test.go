package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// Regression (QA10): the "Type RUN" inputs had no field styling and vanished,
// especially in dark themes.
func TestUICollectionsRunPhraseInputsAreStyledFields(t *testing.T) {
	for _, f := range []string{"js/collections-sheets.js", "js/collections-matrix.js"} {
		src := readUIAsset(t, f)
		if !strings.Contains(src, "'btn btn-field'") {
			t.Fatalf("%s: the RUN confirmation input must carry the btn btn-field styling", f)
		}
	}
	if strings.Contains(readUIAsset(t, "js/collections-sheets.js"), "typed.className = 'input'") {
		t.Fatal("collections-sheets.js: bare .input class has no CSS")
	}
}

// Regression (QA10): the run results table wrapped every cell at narrow widths.
func TestUIRunResultsTableKeepsCellsOnOneLine(t *testing.T) {
	if !strings.Contains(readUIAsset(t, "js/runner.js"), "run-results-tbl") {
		t.Fatal("runner.js must tag the results table")
	}
	if !strings.Contains(readUIAsset(t, "css/collections.css"), ".run-results-tbl th,.run-results-tbl td{white-space:nowrap}") {
		t.Fatal("collections.css must keep run result cells on one line")
	}
}

// Regression (QA10): at 1280px the editor is ~410px wide; its footer actions
// collapsed to icons only below 400px and clipped "Copy as cURL" above it, and
// the hidden-scrollbar tab strip gave no cue that tabs were off-screen.
func TestUIDesktopCollectionsEditorNarrowPaneCues(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-collections.css")
	if !strings.Contains(css, "@container dsk-coll-ed (max-width:480px)") {
		t.Fatal("editor footer must collapse to icons below 480px")
	}
	// Round 2: scroll shadows were not enough ("Settings" still clipped to "Se"
	// next to a badge), so the strip wraps and never hides a tab.
	if strings.Contains(css, "local no-repeat") || strings.Contains(css, "scrollbar-width:none") {
		t.Fatal("the editor tab strip must wrap, not scroll behind a hidden scrollbar")
	}
	if !strings.Contains(readUIAsset(t, "css/collections.css"), ".coll-tabs{flex:none;display:flex;flex-wrap:wrap") {
		t.Fatal("collections.css must let the editor tab strip wrap")
	}
}

// Regression (QA10): restoring a deleted finding reused the revision-restore
// confirmation copy.
func TestUIDeletedFindingRestoreHasItsOwnCopy(t *testing.T) {
	src := readUIAsset(t, "js/finding-revisions.js")
	if !strings.Contains(src, "DELETED_RESTORE_COPY);") || !strings.Contains(src, "Restore this deleted finding?") {
		t.Fatal("the Deleted dialog must confirm with deleted-finding wording")
	}
}

// Regression (QA10): a draft export with readiness gaps carried no Draft marker
// in its body, and the download filenames disagreed between UI and API.
func TestFindingsDraftReportIsMarkedAndNamedDraft(t *testing.T) {
	h, s, _ := newHub(t)
	f := store.Finding{Title: "Gappy", Severity: "High", Status: "open"}
	if _, err := s.CreateFinding(&f); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func(q string) (string, string) {
		resp, err := http.Get(ts.URL + "/api/findings/report" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), resp.Header.Get("Content-Disposition")
	}
	for _, format := range []string{"md", "html"} {
		body, cd := get("?format=" + format + "&mode=draft")
		if !strings.Contains(body, "DRAFT") {
			t.Fatalf("%s draft export has no Draft marker:\n%s", format, body)
		}
		if !strings.Contains(cd, "interseptor-report-draft."+format) {
			t.Fatalf("%s draft filename = %q", format, cd)
		}
	}
}

func TestDraftReportNoteOnlyForGappyDrafts(t *testing.T) {
	gappy := reportQuality{Ready: false, Total: 3, Summary: reportBoardSummary{Blocked: 2}}
	if n := draftReportNote("draft", gappy); !strings.Contains(n, "2 of 3") {
		t.Fatalf("note = %q", n)
	}
	if draftReportNote("final", gappy) != "" || draftReportNote("draft", reportQuality{Ready: true, Total: 3}) != "" || draftReportNote("draft", reportQuality{}) != "" {
		t.Fatal("final, ready and empty reports must not be marked draft")
	}
	// QA round 2: a false positive (always ready, rendered only in the excluded
	// appendix) was counted in "N of M" while the report body listed M-1.
	withFP := assessReportQuality([]store.Finding{
		{ID: 1, Title: "Gap one", Severity: "High", Status: "open"},
		{ID: 2, Title: "Gap two", Severity: "Low", Status: "open"},
		{ID: 3, Title: "Not real", Severity: "Low", Status: "false_positive"},
	})
	if n := draftReportNote("draft", withFP); !strings.Contains(n, "2 of 2 finding(s)") {
		t.Fatalf("draft note must count only reported findings: %q", n)
	}
	// QA round 3: the preflight totals count the same findings as the marker.
	if withFP.Total != 2 || withFP.Summary.Blocked != 2 || withFP.Summary.Ready != 0 || len(withFP.Board) != 3 {
		t.Fatalf("readiness must exclude false positives from its totals: %+v", withFP)
	}
	if reportDisposition("md", "final") != `attachment; filename="interseptor-report.md"` {
		t.Fatal("final filename must stay interseptor-report.md")
	}
}

// Regression (QA10): at 600px the centred command field slid under the
// Intercept pill because the side zones could shrink below their content.
func TestUIDesktopBarCommandFieldYieldsToSidesWhenNarrow(t *testing.T) {
	css := readUIAsset(t, "css/desktop/shell.css")
	if !strings.Contains(css, "@media (max-width:720px){\n  html.desktop-app #bar{grid-template-columns:auto minmax(0,1fr) auto}") {
		t.Fatal("the desktop title bar needs auto side columns below 720px")
	}
}

// Regression (QA10): the 70-character CVSS vector was a one-line input that
// truncated in the 248px properties rail; it is a wrapping field now.
func TestUICvssVectorFieldWraps(t *testing.T) {
	src := readUIAsset(t, "js/cvss.js")
	if !strings.Contains(src, `<textarea id="findCvss"`) || !strings.Contains(src, "event.key === 'Enter'") {
		t.Fatal("the CVSS vector field must wrap and refuse newlines")
	}
}

// Regression (QA10): inline create dropped what was typed when the user chose
// Full form, left the inline row open behind the dialog, and silently cut long titles.
func TestUIInlineNewFindingHandsOffToFullForm(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	for _, want := range []string{"carryToFullForm(", "titleLimitMessage(", "hideInlineNewFinding({ restoreFocus: false });\n  openFindCreate(event);"} {
		if !strings.Contains(src, want) {
			t.Fatalf("findings.js lacks %q", want)
		}
	}
}

// Regression (QA10): at 900px the header's Done and more-menu wrapped onto a second row.
func TestUIFindingHeaderChipCollapsesInNarrowWorkspace(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	if !strings.Contains(css, "@container findws (max-width:520px){\n  .find-workspace .find-header-top .find-mode-chip span{display:none}") {
		t.Fatal("the mode chip must drop its label in a narrow workspace container")
	}
}

// Regression: the Draft marker was prepended above the report title, which hid
// the title from report.WithBranding (it replaces the leading heading) and made
// the marker, not the title, the first line of every export.
func TestDraftNoteSitsUnderTheBrandedTitle(t *testing.T) {
	md := withDraftNote("# Example Corp — External Assessment\n\nbody", "> **DRAFT** - not for delivery")
	if !strings.HasPrefix(md, "# Example Corp — External Assessment\n\n> **DRAFT**") {
		t.Fatalf("draft note must follow the title line, got %q", md)
	}
	if got := withDraftNote("no heading", "> **DRAFT**"); !strings.HasPrefix(got, "> **DRAFT**\n\nno heading") {
		t.Fatalf("without a title the note leads, got %q", got)
	}
	if got := withDraftNote("# T\n\nbody", ""); got != "# T\n\nbody" {
		t.Fatalf("an empty note changes nothing, got %q", got)
	}
}
