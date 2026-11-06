package playbooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledCorpusLoadsAndIsTrusted(t *testing.T) {
	all, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) < 5 {
		t.Fatalf("bundled corpus has %d playbooks, want the shipped set", len(all))
	}
	for _, p := range all {
		if p.Source != SourceBundled || !p.Trusted {
			t.Fatalf("%s: source=%s trusted=%v", p.ID, p.Source, p.Trusted)
		}
		if p.Title == "" || p.Body == "" {
			t.Fatalf("%s: empty title or body", p.ID)
		}
		if len(p.AppliesTo) == 0 {
			t.Fatalf("%s: no applies_to signals", p.ID)
		}
		if p.Invalid != "" {
			t.Fatalf("%s: %s", p.ID, p.Invalid)
		}
	}
}

func TestProjectPlaybookOverridesBundledByID(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `---
id: object-authz
title: House IDOR rules
applies_to:
  - param:invoice_id
---

Check invoice ids specifically.
`
	if err := os.WriteFile(filepath.Join(dir, "playbooks", "house-idor.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var got Playbook
	for _, p := range all {
		if p.ID == "object-authz" {
			got = p
		}
	}
	if got.Title != "House IDOR rules" {
		t.Fatalf("project playbook did not win: %+v", got)
	}
	if got.Source != SourceProject || got.Trusted {
		t.Fatalf("a project playbook must be untrusted: source=%s trusted=%v", got.Source, got.Trusted)
	}
	if !strings.Contains(got.Rendered(), "UNTRUSTED") {
		t.Fatal("the rendered project playbook must carry its trust warning")
	}
}

func TestBrokenProjectPlaybookStaysVisible(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "playbooks", "broken.md"), []byte("---\nid: [\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var found bool
	for _, p := range all {
		if p.ID == "broken" {
			found = true
			if p.Invalid == "" {
				t.Fatalf("broken playbook should report its error: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("a broken project playbook must not vanish from the list")
	}
}

func TestMatchSignals(t *testing.T) {
	p := Playbook{Frontmatter: Frontmatter{ID: "x", AppliesTo: []string{
		"path:/checkout", "param:price", "mime:text/html", "header:authorization", "tech:graphql",
	}}}
	cases := []struct {
		name string
		o    Observed
		want bool
	}{
		{"path prefix", Observed{Path: "/checkout/pay"}, true},
		{"param name", Observed{Params: []string{"price"}}, true},
		{"mime", Observed{Mime: "text/html; charset=utf-8"}, true},
		{"header present", Observed{Headers: map[string]string{"Authorization": "Bearer x"}}, true},
		{"tech in body", Observed{Body: "graphql"}, true},
		{"nothing", Observed{Path: "/health", Method: "GET"}, false},
	}
	for _, c := range cases {
		if got := p.Match(c.o) != ""; got != c.want {
			t.Fatalf("%s: match=%v want %v (%+v)", c.name, got, c.want, c.o)
		}
	}
}

func TestParseSignalFallsBackToTech(t *testing.T) {
	if s := ParseSignal("param:user_id"); s.Kind != "param" || s.Value != "user_id" {
		t.Fatalf("parsed = %+v", s)
	}
	if s := ParseSignal("graphql"); s.Kind != "tech" || s.Value != "graphql" {
		t.Fatalf("parsed = %+v", s)
	}
	if s := ParseSignal("unknownkind:value"); s.Kind != "tech" {
		t.Fatalf("unknown kind should be a tech hint: %+v", s)
	}
}
