package playbooks

import (
	"strings"
	"testing"
)

// The body must start after the closing --- line, not on it, and must never
// contain frontmatter.
func TestParseBodyExcludesFrontmatterTerminator(t *testing.T) {
	for name, src := range map[string]string{
		"lf":           "---\nid: x\ntitle: T\n---\nbody line\n",
		"crlf":         "---\r\nid: x\r\ntitle: T\r\n---\r\nbody line\r\n",
		"indented":     "  ---\nid: x\ntitle: T\n---\nbody line\n",
		"leading-nl":   "\n\n---\nid: x\ntitle: T\n---\nbody line\n",
		"bom":          "\ufeff---\nid: x\ntitle: T\n---\nbody line\n",
		"blank-inside": "---\nid: x\n\ntitle: T\n---\nbody line\n",
		"no-final-nl":  "---\nid: x\ntitle: T\n---\nbody line",
		"crlf-no-nl":   "---\r\nid: x\r\ntitle: T\r\n---\r\nbody line",
		"padded-close": "---\nid: x\ntitle: T\n---  \nbody line\n",
	} {
		p, err := Parse("file.md", src, SourceProject)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.ID != "x" || p.Title != "T" {
			t.Errorf("%s: id=%q title=%q", name, p.ID, p.Title)
		}
		if p.Body != "body line" {
			t.Errorf("%s: body = %q, want %q", name, p.Body, "body line")
		}
	}
	// The closing --- as the last bytes of the file, with and without CRLF.
	for _, src := range []string{"---\nid: x\n---", "---\r\nid: x\r\n---", "---\nid: x\n---\n"} {
		p, err := Parse("file.md", src, SourceProject)
		if err != nil || p.ID != "x" || p.Body != "" {
			t.Fatalf("%q: id=%q body=%q err=%v", src, p.ID, p.Body, err)
		}
	}
	if _, err := Parse("file.md", "---\nid: x\nbody", SourceProject); err == nil {
		t.Fatal("an unterminated frontmatter block must be an error")
	}
}

func TestBundledBodiesDoNotStartWithTheTerminator(t *testing.T) {
	all, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if strings.HasPrefix(p.Body, "---") {
			t.Errorf("%s: body starts with the frontmatter terminator: %q", p.ID, p.Body[:20])
		}
	}
}
