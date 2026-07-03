package control

import (
	"path/filepath"
	"testing"
)

func TestIsSafeExternalPath(t *testing.T) {
	if isSafeExternalPath("") {
		t.Fatal("empty path must be rejected")
	}
	if isSafeExternalPath(string(filepath.Separator)) {
		t.Fatal("filesystem root must be rejected")
	}
	if vol := filepath.VolumeName(`C:\foo`); vol != "" {
		if isSafeExternalPath(vol + string(filepath.Separator)) {
			t.Fatal("drive root must be rejected")
		}
		if isSafeExternalPath(vol) {
			t.Fatal("bare drive letter must be rejected")
		}
	}
	if !isSafeExternalPath(filepath.Join(t.TempDir(), "acme-engagement")) {
		t.Fatal("an ordinary absolute directory must be accepted")
	}
}

func TestRememberExternalProjectDedupesAndCaps(t *testing.T) {
	dir := t.TempDir()
	rememberExternalProject(dir, "a", filepath.Join(dir, "a"))
	rememberExternalProject(dir, "b", filepath.Join(dir, "b"))
	rememberExternalProject(dir, "a", filepath.Join(dir, "a")) // re-use moves to front, no duplicate

	list := readExternalProjects(dir)
	if len(list) != 2 {
		t.Fatalf("expected 2 deduped entries, got %d: %+v", len(list), list)
	}
	if list[0].Name != "a" {
		t.Fatalf("expected most-recently-used first, got %+v", list)
	}

	for i := 0; i < maxExternalProjects+5; i++ {
		rememberExternalProject(dir, "p", filepath.Join(dir, "distinct", string(rune('a'+i%26)), string(rune('0'+i%10))))
	}
	if got := len(readExternalProjects(dir)); got > maxExternalProjects {
		t.Fatalf("expected the list capped at %d, got %d", maxExternalProjects, got)
	}
}
