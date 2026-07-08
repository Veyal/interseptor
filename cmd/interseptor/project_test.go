package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectDir(t *testing.T) {
	// Use OS-absolute paths so the assertions hold on every platform (on Windows
	// a bare "/home/u" is not absolute and filepath.Abs would prepend the drive).
	home := t.TempDir()
	projects := filepath.Join(home, ".interseptor", "projects")

	// a bare name lands under projects/
	if name, dir := resolveProjectDir(projects, "acme", home); name != "acme" || dir != filepath.Join(projects, "acme") {
		t.Fatalf("name: got (%q,%q)", name, dir)
	}
	// an absolute path is used verbatim; name is its base. Use a real OS-absolute
	// path (a bare "/tmp/..." is not absolute on Windows and would be resolved
	// against the current drive, so it can't be asserted verbatim cross-platform).
	absIn := filepath.Join(t.TempDir(), "scan1")
	if name, dir := resolveProjectDir(projects, absIn, home); name != "scan1" || dir != absIn {
		t.Fatalf("abs: got (%q,%q)", name, dir)
	}
	// ~ expands to home
	if _, dir := resolveProjectDir(projects, "~/proj", home); dir != filepath.Join(home, "proj") {
		t.Fatalf("home: got %q", dir)
	}
}

func TestSanitizeProjectName(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"acme", true}, {"my-scan_1.bak", true}, {"", false},
		{".", false}, {"..", false}, {"a/b", false}, {`a\b`, false}, {"  ", false},
	} {
		if _, err := sanitizeProjectName(c.in); (err == nil) != c.ok {
			t.Errorf("sanitizeProjectName(%q): ok=%v want %v", c.in, err == nil, c.ok)
		}
	}
}

func TestIsBareProjectName(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"acme", true}, {"default", true}, {"my-scan_1", true},
		{"", false}, {".", false}, {"..", false},
		{"a/b", false}, {`a\b`, false}, {"~/x", false}, {"-flag", false},
	} {
		if got := isBareProjectName(c.in); got != c.ok {
			t.Errorf("isBareProjectName(%q) = %v want %v", c.in, got, c.ok)
		}
	}
}

func TestListProjects(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	for _, n := range []string{"beta", "alpha"} {
		if err := os.MkdirAll(filepath.Join(projects, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a stray file must be ignored, and results are sorted
	os.WriteFile(filepath.Join(projects, "notadir.txt"), []byte("x"), 0o644)
	got := listProjects(projects)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("listProjects = %v", got)
	}
	// missing dir → empty, no error
	if got := listProjects(filepath.Join(root, "nope")); len(got) != 0 {
		t.Fatalf("missing projects dir = %v", got)
	}
}

func TestSelectProjectDefault(t *testing.T) {
	root := t.TempDir()
	// No flag and no remembered project → the default project, which is the
	// global root itself (backward compatible with single-project installs).
	name, dir, err := selectProject(root, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" || dir != root {
		t.Fatalf("got (%q,%q), want (default,%q)", name, dir, root)
	}
}

func TestSelectProjectFlag(t *testing.T) {
	root := t.TempDir()
	name, dir, err := selectProject(root, "acme", root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "projects", "acme")
	if name != "acme" || dir != want {
		t.Fatalf("got (%q,%q), want (acme,%q)", name, dir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("project dir not created: %v", err)
	}
}

func TestSelectProjectDefaultFlagIsRoot(t *testing.T) {
	root := t.TempDir()
	// --project default (or switching back to "default") must return to the
	// global root, not a separate projects/default — otherwise switching away
	// and back would silently orphan the original project's data.
	name, dir, err := selectProject(root, "default", root)
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" || dir != root {
		t.Fatalf("--project default must map to the root, got (%q,%q)", name, dir)
	}
}

func TestSelectProjectResumesLastProject(t *testing.T) {
	root := t.TempDir()
	// The UI switched to "saved1" earlier, recorded in active-project. A plain
	// launch (no flag) must resume it instead of falling back to default.
	writeLastProject(root, "saved1")
	name, dir, err := selectProject(root, "", root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "projects", "saved1")
	if name != "saved1" || dir != want {
		t.Fatalf("resume: got (%q,%q), want (saved1,%q)", name, dir, want)
	}
	// An explicit flag still overrides the remembered project.
	if name, _, _ := selectProject(root, "default", root); name != "default" {
		t.Fatalf("flag should override remembered project, got %q", name)
	}
}
