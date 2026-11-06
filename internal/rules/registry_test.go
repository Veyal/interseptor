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

	// Build a pack from a source dir, then install it via the registry.
	src := t.TempDir()
	writeCheckDir(t, src)
	var buf bytes.Buffer
	if _, err := BuildPack(src, Manifest{Name: "owasp", Version: "1.0.0", Author: "a"}, &buf); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root)
	m, n, err := reg.InstallStreamOpts(bytes.NewReader(buf.Bytes()), checksDir, "test", InstallOpts{AllowUnsigned: true})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if m.Name != "owasp" || n != 2 {
		t.Fatalf("install returned %s/%d", m.Name, n)
	}
	// Checks landed on disk in the right dirs.
	if _, err := os.Stat(filepath.Join(checksDir, "hsts.star")); err != nil {
		t.Fatalf("passive check not installed: %v", err)
	}

	packs, err := reg.List()
	if err != nil || len(packs) != 1 || packs[0].Name != "owasp" {
		t.Fatalf("list wrong: %v %v", packs, err)
	}

	removed, err := reg.Remove("owasp", checksDir)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 files removed, got %d", removed)
	}
	if _, err := os.Stat(filepath.Join(checksDir, "hsts.star")); !os.IsNotExist(err) {
		t.Fatalf("passive check should be gone after remove")
	}
	packs, _ = reg.List()
	if len(packs) != 0 {
		t.Fatalf("pack should be unregistered after remove, got %v", packs)
	}
}

// Upgrading a pack must remove the checks the previous version owned and the
// new version dropped. Otherwise the old check file stays active forever: the
// new registry record no longer lists it, so `rules remove` cannot delete it.
func TestRegistryUpgradeRemovesDroppedChecks(t *testing.T) {
	root := t.TempDir()
	checksDir := filepath.Join(root, "checks")
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "checks"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "checks", "keep.star"), []byte("def check(flow):\n    return []\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(src, "checks", "drop.star"), []byte("def check(flow):\n    return []\n"), 0o644))

	reg := NewRegistry(root)
	install := func(v string) {
		var buf bytes.Buffer
		if _, err := BuildPack(src, Manifest{Name: "p", Version: v}, &buf); err != nil {
			t.Fatal(err)
		}
		if _, _, err := reg.InstallStreamOpts(bytes.NewReader(buf.Bytes()), checksDir, "test", InstallOpts{AllowUnsigned: true}); err != nil {
			t.Fatal(err)
		}
	}
	install("1.0.0")
	must(t, os.Remove(filepath.Join(src, "checks", "drop.star")))
	install("1.1.0")

	if _, err := os.Stat(filepath.Join(checksDir, "drop.star")); !os.IsNotExist(err) {
		t.Fatalf("a check dropped by the upgrade is still installed (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(checksDir, "keep.star")); err != nil {
		t.Fatalf("the kept check was removed: %v", err)
	}
	rec, ok, _ := reg.Get("p")
	if !ok || len(rec.IDs) != 1 || rec.IDs[0] != "keep" {
		t.Fatalf("registry ids = %+v, want [keep]", rec)
	}
}

func TestRegistryUpgradeReplacesEntry(t *testing.T) {
	root := t.TempDir()
	checksDir := filepath.Join(root, "checks")
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "checks"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "checks", "a.star"), []byte("def check(flow):\n    return []\n"), 0o644))

	reg := NewRegistry(root)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		var buf bytes.Buffer
		if _, err := BuildPack(src, Manifest{Name: "p", Version: v}, &buf); err != nil {
			t.Fatal(err)
		}
		if _, _, err := reg.InstallStreamOpts(bytes.NewReader(buf.Bytes()), checksDir, "test", InstallOpts{AllowUnsigned: true}); err != nil {
			t.Fatal(err)
		}
	}
	packs, _ := reg.List()
	if len(packs) != 1 || packs[0].Version != "1.1.0" {
		t.Fatalf("upgrade should replace the entry, got %+v", packs)
	}
}
