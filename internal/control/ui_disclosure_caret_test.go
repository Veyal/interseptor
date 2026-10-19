package control

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Regression: surfaces.css draws a disclosure arrow on every details>summary.
// A summary that renders its own caret icon (Findings reproduction steps and the
// report-prep checklist) showed two arrows. Every such summary class must switch
// the generic arrow off.
func TestUISummariesWithOwnCaretHideTheGenericArrow(t *testing.T) {
	summary := regexp.MustCompile(`<summary class="([a-z0-9-]+)">[^<]{0,40}<svg[^>]*class="[^"]*(?:caret|chevron)`)
	var sources, css strings.Builder
	err := fs.WalkDir(uiFS, "ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(path, "/_js-tests/") {
			return err
		}
		body, err := fs.ReadFile(uiFS, path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(path, ".html"), strings.HasSuffix(path, ".js"):
			sources.Write(body)
		case strings.HasSuffix(path, ".css"):
			css.Write(body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := summary.FindAllStringSubmatch(sources.String(), -1)
	if len(found) == 0 {
		t.Fatal("expected at least one summary with its own caret (find-xchhd, find-prep-sum)")
	}
	for _, m := range found {
		if !strings.Contains(css.String(), "."+m[1]+"::before{display:none}") {
			t.Errorf("summary.%s draws its own caret but keeps the generic details>summary arrow; add .%s::before{display:none}", m[1], m[1])
		}
	}
}
