package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateDataDirMovesOldToNew covers the primary rebrand path: an existing
// ~/.interceptor install must be picked up under ~/.interseptor on first run,
// with its contents intact and nothing left behind.
func TestMigrateDataDirMovesOldToNew(t *testing.T) {
	home := t.TempDir()
	oldDir := filepath.Join(home, oldDataDirName)
	if err := os.MkdirAll(filepath.Join(oldDir, "projects", "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(oldDir, "projects", "default", "marker.txt")
	if err := os.WriteFile(marker, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateDataDir(home); err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	}

	newDir := filepath.Join(home, newDataDirName)
	got, err := os.ReadFile(filepath.Join(newDir, "projects", "default", "marker.txt"))
	if err != nil {
		t.Fatalf("marker file missing under new dir: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("marker content = %q, want %q", got, "hello")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old dir %s should no longer exist, stat err = %v", oldDir, err)
	}
}

// TestMigrateDataDirNoopWhenNeitherExists covers a fresh install: nothing to
// migrate, no error, no directories created.
func TestMigrateDataDirNoopWhenNeitherExists(t *testing.T) {
	home := t.TempDir()
	if err := migrateDataDir(home); err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, oldDataDirName)); !os.IsNotExist(err) {
		t.Fatalf("old dir should not have been created")
	}
	if _, err := os.Stat(filepath.Join(home, newDataDirName)); !os.IsNotExist(err) {
		t.Fatalf("new dir should not have been created")
	}
}

// TestMigrateDataDirSkipsWhenBothExist covers an already-migrated (or
// manually-populated) install: migration must never merge or overwrite, so
// both directories are left exactly as they were.
func TestMigrateDataDirSkipsWhenBothExist(t *testing.T) {
	home := t.TempDir()
	oldDir := filepath.Join(home, oldDataDirName)
	newDir := filepath.Join(home, newDataDirName)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "old-marker.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "new-marker.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateDataDir(home); err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(oldDir, "old-marker.txt")); err != nil {
		t.Fatalf("old dir contents must survive untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "new-marker.txt")); err != nil {
		t.Fatalf("new dir contents must survive untouched: %v", err)
	}
	// migration must not have merged old-marker.txt into newDir
	if _, err := os.Stat(filepath.Join(newDir, "old-marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("migration must never merge into an existing new dir")
	}
}

// TestMigrateDataDirFailureIsSurfaced covers a migration that cannot complete
// (here: the new dir's parent is a regular file, so neither rename nor the
// copy fallback can create anything under it) — the error must be returned,
// not swallowed, and the old directory must be left in place so no data is lost.
func TestMigrateDataDirFailureIsSurfaced(t *testing.T) {
	home := t.TempDir()
	oldDir := filepath.Join(home, "blocked-old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "marker.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// blocker is a regular file standing where a directory component of newDir
	// needs to be — this fails both os.Rename and the recursive-copy fallback
	// on every OS, without relying on OS-specific permission semantics.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(blocker, newDataDirName)

	err := migrateDir(oldDir, newDir)
	if err == nil {
		t.Fatal("expected migrateDir to return an error, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(oldDir, "marker.txt")); statErr != nil {
		t.Fatalf("old dir must be left intact on failure: %v", statErr)
	}
}
