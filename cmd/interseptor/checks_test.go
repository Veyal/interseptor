package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Veyal/interseptor/internal/checkscript"
)

func TestMigrateGlobalChecks(t *testing.T) {
	global := t.TempDir()
	projects := filepath.Join(global, "projects")
	projA := filepath.Join(projects, "alpha", "checks")
	projB := filepath.Join(projects, "beta", "checks")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projA, "from-a.star"), []byte("def check(flow):\n    return []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, "from-b.star"), []byte("def check(flow):\n    return []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateGlobalChecks(global, projects)

	globalChecks := filepath.Join(global, "checks")
	for _, name := range []string{"from-a.star", "from-b.star"} {
		if _, err := os.Stat(filepath.Join(globalChecks, name)); err != nil {
			t.Fatalf("missing merged %s: %v", name, err)
		}
	}
	// Second run must not duplicate or error.
	migrateGlobalChecks(global, projects)
	list := checkscript.List(globalChecks)
	if len(list) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(list))
	}
}

func TestMergeDirDoesNotOverwrite(t *testing.T) {
	dst := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "keep.star"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.star"), []byte("project"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "new.star"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := checkscript.MergeDir(src, dst)
	if err != nil || n != 1 {
		t.Fatalf("MergeDir = %d, %v", n, err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "keep.star"))
	if string(got) != "global" {
		t.Fatalf("global check overwritten: %q", got)
	}
}
