package playbooks

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func r3ProjectDir(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "playbooks"), 0o755); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < n; i++ {
		src := fmt.Sprintf("---\nid: proj-%d\ntitle: Project %d\napplies_to:\n  - param:redirect_url\n---\nBody %d\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, "playbooks", fmt.Sprintf("p%d.md", i)), []byte(src), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

func BenchmarkR3LoadBundled(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Load(""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkR3LoadWithProject(b *testing.B) {
	dir := r3ProjectDir(b, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(dir); err != nil {
			b.Fatal(err)
		}
	}
}

// A readiness-style call: load, then match every playbook against a handful
// of observations.
func BenchmarkR3LoadAndMatch(b *testing.B) {
	obs := []Observed{
		{Method: "GET", Host: "example.com", Path: "/login", Params: []string{"redirect_url"}},
		{Method: "POST", Host: "api.example.com", Path: "/graphql", Mime: "application/json"},
		{Method: "GET", Host: "example.com", Path: "/api/users/1", Headers: map[string]string{"Authorization": "Bearer x"}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		all, err := Load("")
		if err != nil {
			b.Fatal(err)
		}
		for _, p := range all {
			for _, o := range obs {
				_ = p.Match(o)
			}
		}
	}
}

// The bundled set is parsed once; what Load hands out is the caller's own
// copy, so changing it cannot leak into a later Load.
func TestR3LoadReturnsIndependentCopies(t *testing.T) {
	first, err := Load("")
	if err != nil || len(first) == 0 {
		t.Fatalf("Load: %v (%d)", err, len(first))
	}
	var idx int
	for i, p := range first {
		if len(p.AppliesTo) > 0 {
			idx = i
		}
	}
	wantID, wantSig, wantBody := first[idx].ID, first[idx].AppliesTo[0], first[idx].Body
	first[idx].Title = "changed"
	first[idx].Body = "changed"
	first[idx].AppliesTo[0] = "host:changed.example.com"
	first[idx].AppliesTo = append(first[idx].AppliesTo, "tech:extra")
	second, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	got := second[idx]
	if got.ID != wantID || got.Title == "changed" || got.Body != wantBody || got.AppliesTo[0] != wantSig {
		t.Fatalf("a caller's change leaked into the next Load: %+v", got.Summarize())
	}
	if !got.Trusted || got.Source != SourceBundled {
		t.Fatalf("bundled playbook lost its provenance: %+v", got.Summarize())
	}
}

// Project files are read on every Load: an edit, a new file and a removal are
// all visible at once, and a project id still overrides a bundled one.
func TestR3ProjectPlaybooksAreNotCached(t *testing.T) {
	dir := r3ProjectDir(t, 1)
	path := filepath.Join(dir, "playbooks", "p0.md")
	if p, ok := Get(dir, "proj-0"); !ok || p.Title != "Project 0" || p.Trusted {
		t.Fatalf("project playbook: %+v %v", p.Summarize(), ok)
	}
	if err := os.WriteFile(path, []byte("---\nid: proj-0\ntitle: Edited\n---\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := Get(dir, "proj-0"); p.Title != "Edited" {
		t.Fatalf("edit not visible: %q", p.Title)
	}
	bundledID := ""
	all, _ := Load("")
	bundledID = all[0].ID
	if err := os.WriteFile(path, []byte("---\nid: "+bundledID+"\ntitle: Override\n---\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := Get(dir, bundledID); p.Title != "Override" || p.Trusted || p.Source != SourceProject {
		t.Fatalf("project override: %+v", p.Summarize())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if p, _ := Get(dir, bundledID); !p.Trusted || p.Source != SourceBundled {
		t.Fatalf("bundled playbook not restored after the override was removed: %+v", p.Summarize())
	}
}
