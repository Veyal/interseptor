package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProjectDir(t *testing.T) {
	home := "/home/u"
	projects := "/home/u/.interceptor/projects"

	// a bare name lands under projects/
	if name, dir := resolveProjectDir(projects, "acme", home); name != "acme" || dir != filepath.Join(projects, "acme") {
		t.Fatalf("name: got (%q,%q)", name, dir)
	}
	// an absolute path is used verbatim; name is its base
	if name, dir := resolveProjectDir(projects, "/tmp/work/scan1", home); name != "scan1" || dir != "/tmp/work/scan1" {
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

func TestSelectProjectNonInteractiveDefault(t *testing.T) {
	root := t.TempDir()
	name, dir, err := selectProject(strings.NewReader(""), &strings.Builder{}, root, "", root, false)
	if err != nil {
		t.Fatal(err)
	}
	// default project keeps using the global root (backward compatible)
	if name != "default" || dir != root {
		t.Fatalf("got (%q,%q), want (default,%q)", name, dir, root)
	}
}

func TestSelectProjectFlag(t *testing.T) {
	root := t.TempDir()
	name, dir, err := selectProject(strings.NewReader(""), &strings.Builder{}, root, "acme", root, false)
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

func TestSelectProjectInteractiveEnterIsDefault(t *testing.T) {
	root := t.TempDir()
	name, dir, err := selectProject(strings.NewReader("\n"), &strings.Builder{}, root, "", root, true)
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" || dir != root {
		t.Fatalf("Enter should pick default; got (%q,%q)", name, dir)
	}
}

func TestSelectProjectInteractiveNew(t *testing.T) {
	root := t.TempDir()
	// choose "1" (new), then type a name
	name, dir, err := selectProject(strings.NewReader("1\nbrandnew\n"), &strings.Builder{}, root, "", root, true)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "projects", "brandnew")
	if name != "brandnew" || dir != want {
		t.Fatalf("got (%q,%q), want (brandnew,%q)", name, dir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("new project dir not created: %v", err)
	}
}

func TestSelectProjectInteractiveContinue(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects", "saved1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// choose "2" (continue), then pick item "1" from the list
	name, dir, err := selectProject(strings.NewReader("2\n1\n"), &strings.Builder{}, root, "", root, true)
	if err != nil {
		t.Fatal(err)
	}
	if name != "saved1" || dir != filepath.Join(root, "projects", "saved1") {
		t.Fatalf("got (%q,%q), want saved1", name, dir)
	}
}

func TestSelectProjectQuit(t *testing.T) {
	root := t.TempDir()
	_, _, err := selectProject(strings.NewReader("q\n"), &strings.Builder{}, root, "", root, true)
	if err != errQuit {
		t.Fatalf("quit: got err=%v want errQuit", err)
	}
}
