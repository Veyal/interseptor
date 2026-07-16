package rules

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryInstallListRemove(t *testing.T) {
	root := t.TempDir()
	checksDir := filepath.Join(root, "checks")
	activeDir := filepath.Join(root, "active-checks")

	// Build a pack from a source dir, then install it via the registry.
	src := t.TempDir()
	writeCheckDir(t, src)
	var buf bytes.Buffer
	if _, err := BuildPack(src, Manifest{Name: "owasp", Version: "1.0.0", Author: "a"}, &buf); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root)
	m, n, err := reg.InstallStream(bytes.NewReader(buf.Bytes()), checksDir, activeDir, "test")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if m.Name != "owasp" || n != 3 {
		t.Fatalf("install returned %s/%d", m.Name, n)
	}
	// Checks landed on disk in the right dirs.
	if _, err := os.Stat(filepath.Join(checksDir, "hsts.star")); err != nil {
		t.Fatalf("passive check not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(activeDir, "sqli.star")); err != nil {
		t.Fatalf("active check not installed: %v", err)
	}

	packs, err := reg.List()
	if err != nil || len(packs) != 1 || packs[0].Name != "owasp" {
		t.Fatalf("list wrong: %v %v", packs, err)
	}

	removed, err := reg.Remove("owasp", checksDir, activeDir)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 files removed, got %d", removed)
	}
	if _, err := os.Stat(filepath.Join(checksDir, "hsts.star")); !os.IsNotExist(err) {
		t.Fatalf("passive check should be gone after remove")
	}
	packs, _ = reg.List()
	if len(packs) != 0 {
		t.Fatalf("pack should be unregistered after remove, got %v", packs)
	}
}

func TestRegistryUpgradeReplacesEntry(t *testing.T) {
	root := t.TempDir()
	checksDir := filepath.Join(root, "checks")
	activeDir := filepath.Join(root, "active-checks")
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "checks"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "checks", "a.star"), []byte("def check(flow):\n    return []\n"), 0o644))

	reg := NewRegistry(root)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		var buf bytes.Buffer
		if _, err := BuildPack(src, Manifest{Name: "p", Version: v}, &buf); err != nil {
			t.Fatal(err)
		}
		if _, _, err := reg.InstallStream(bytes.NewReader(buf.Bytes()), checksDir, activeDir, "test"); err != nil {
			t.Fatal(err)
		}
	}
	packs, _ := reg.List()
	if len(packs) != 1 || packs[0].Version != "1.1.0" {
		t.Fatalf("upgrade should replace the entry, got %+v", packs)
	}
}
