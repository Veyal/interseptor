package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The desktop shell starts the server with no flags. With the resume fallback
// it must come up in "default" when the remembered external folder is gone,
// without creating that folder and without forgetting it.
func TestResumeFallbackOpensDefaultAndKeepsRememberedFolder(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "unplugged", "acme")
	writeLastProject(root, missing)

	sel, err := selectStartupProject(root, "", root, resumeFallbackDefault)
	if err != nil {
		t.Fatalf("fallback start must not fail: %v", err)
	}
	if sel.name != "default" || sel.dir != root {
		t.Fatalf("got (%q, %q), want the default project at %q", sel.name, sel.dir, root)
	}
	if !strings.Contains(sel.notice, missing) {
		t.Fatalf("notice must name the missing folder, got %q", sel.notice)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing folder was created: %v", err)
	}
	sel.remember(root, "")
	if got := readLastProject(root); got != missing {
		t.Fatalf("remembered project = %q, want it left as %q so a reconnect resumes it", got, missing)
	}

	// The folder comes back: the next plain start resumes it, with no notice.
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	sel, err = selectStartupProject(root, "", root, resumeFallbackDefault)
	if err != nil || sel.dir != missing || sel.notice != "" {
		t.Fatalf("after reconnect: got (%+v, %v), want %q resumed without a notice", sel, err, missing)
	}
}

// A remembered path that is now a regular file falls back the same way.
func TestResumeFallbackCoversNonDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLastProject(root, file)
	sel, err := selectStartupProject(root, "", root, resumeFallbackDefault)
	if err != nil || sel.name != "default" || sel.notice == "" {
		t.Fatalf("got (%+v, %v), want default with a notice", sel, err)
	}
}

// The plain CLI keeps the owner's behaviour: report, never replace.
func TestResumeWithoutFallbackStillFails(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	writeLastProject(root, missing)
	sel, err := selectStartupProject(root, "", root, "")
	if err == nil {
		t.Fatalf("plain start must report the missing folder, got %+v", sel)
	}
	if !strings.Contains(err.Error(), "--project default") {
		t.Fatalf("error lost its recovery hint: %v", err)
	}
	if got := readLastProject(root); got != missing {
		t.Fatalf("remembered project changed to %q", got)
	}
}

// The fallback only covers a missing *remembered* folder. An explicit project
// and an ordinary resume behave exactly as before and are remembered.
func TestResumeFallbackLeavesOtherStartsAlone(t *testing.T) {
	root := t.TempDir()
	sel, err := selectStartupProject(root, "acme", root, resumeFallbackDefault)
	if err != nil || sel.name != "acme" || sel.notice != "" {
		t.Fatalf("explicit project: got (%+v, %v)", sel, err)
	}
	sel.remember(root, "acme")
	if got := readLastProject(root); got != "acme" {
		t.Fatalf("remembered %q, want acme", got)
	}
	sel, err = selectStartupProject(root, "", root, resumeFallbackDefault)
	if err != nil || sel.name != "acme" || sel.notice != "" {
		t.Fatalf("plain resume: got (%+v, %v)", sel, err)
	}
}

func TestResolveResumeFallback(t *testing.T) {
	for _, c := range []struct {
		flag, env, want string
		bad             bool
	}{
		{"", "", "", false},
		{"default", "", resumeFallbackDefault, false},
		{"", " Default ", resumeFallbackDefault, false},
		{"default", "nonsense", resumeFallbackDefault, false}, // the flag wins
		{"projects/x", "", "", true},
		{"", "nonsense", "", true},
	} {
		got, err := resolveResumeFallback(c.flag, c.env)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("resolveResumeFallback(%q, %q) = (%q, %v), want (%q, bad=%v)", c.flag, c.env, got, err, c.want, c.bad)
		}
	}
}

// A project-switch or self-update restart must carry the option along.
func TestReexecKeepsResumeFallbackFlag(t *testing.T) {
	got := strings.Join(reexecArgs([]string{"--resume-fallback=default", "--project", "a"}, "b"), " ")
	if got != "--resume-fallback=default --project b" {
		t.Fatalf("reexecArgs = %q", got)
	}
}

// A self-update restart of a fallback start is launched with --project default.
// It must not record "default" over the folder that is still remembered.
func TestRestartOfFallbackStartKeepsRememberedFolder(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "unplugged")
	writeLastProject(root, missing)

	sel, err := selectStartupProject(root, "default", root, resumeFallbackDefault)
	if err != nil {
		t.Fatal(err)
	}
	sel = carryResumeFallback(sel, root, true)
	sel.remember(root, "default")
	if got := readLastProject(root); got != missing {
		t.Fatalf("remembered project = %q, want %q", got, missing)
	}
	if sel.notice == "" {
		t.Error("the restarted process lost the notice")
	}

	// Without the marker, an explicit --project default is the operator's choice.
	sel, _ = selectStartupProject(root, "default", root, resumeFallbackDefault)
	sel = carryResumeFallback(sel, root, false)
	sel.remember(root, "default")
	if got := readLastProject(root); got != "default" {
		t.Fatalf("explicit default was not remembered: %q", got)
	}
}
