package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A second instance (the launcher starts one per project) may rewrite
// active-project between selectProject and rememberActiveProject. What is saved
// must describe the project this process opened, not the other writer's value.
func TestRememberActiveProjectIgnoresConcurrentRewrite(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "engagement")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLastProject(root, external)
	name, dir, err := selectProject(root, "", root)
	if err != nil || dir != external {
		t.Fatalf("resume external: (%q, %q, %v)", name, dir, err)
	}
	writeLastProject(root, "other") // another instance starts meanwhile

	rememberActiveProject(root, "", name, dir)

	if got := readLastProject(root); got != external {
		t.Fatalf("remembered %q, want the opened directory %q", got, external)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "engagement")); !os.IsNotExist(err) {
		t.Fatalf("a same-named internal project must not appear: %v", err)
	}
}

// The reverse interleaving: this process resumed "default" while the file now
// holds a path. Default must stay "default", not become the data dir's basename.
func TestRememberActiveProjectKeepsDefaultByName(t *testing.T) {
	root := t.TempDir()
	name, dir, err := selectProject(root, "", root)
	if err != nil || name != "default" {
		t.Fatalf("select default: (%q, %q, %v)", name, dir, err)
	}
	writeLastProject(root, filepath.Join(t.TempDir(), "elsewhere"))
	rememberActiveProject(root, "", name, dir)
	if got := readLastProject(root); got != "default" {
		t.Fatalf("remembered %q, want default", got)
	}
	// A blank INTERSEPTOR_PROJECT resumes too and must behave the same.
	rememberActiveProject(root, "  ", name, dir)
	if got := readLastProject(root); got != "default" {
		t.Fatalf("blank project arg remembered %q, want default", got)
	}
}

func TestWriteLastProjectIsPrivateAndLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	writeLastProject(root, "/clients/acme")
	writeLastProject(root, "/clients/acme2")
	fi, err := os.Stat(lastProjectPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("active-project mode %v exposes a client folder path to other users", perm)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestResumeMissingExternalProjectSaysHowToRecover(t *testing.T) {
	root := t.TempDir()
	writeLastProject(root, filepath.Join(t.TempDir(), "gone"))
	_, _, err := selectProject(root, "", root)
	if err == nil || !strings.Contains(err.Error(), "--project default") {
		t.Fatalf("error must name the way out, got %v", err)
	}
}

// "--project ." is the current directory, not the projects folder itself.
func TestResolveProjectDirDotIsADirectory(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if name, dir := resolveProjectDir(projects, ".", "/home/x"); dir != cwd || name != filepath.Base(cwd) {
		t.Fatalf(`"." resolved to (%q, %q), want the working directory %q`, name, dir, cwd)
	}
	if _, dir := resolveProjectDir(projects, "..", "/home/x"); dir != filepath.Dir(cwd) {
		t.Fatalf(`".." resolved to %q, want %q`, dir, filepath.Dir(cwd))
	}
}

// The checks/rules subcommands were removed with the passive scanner; the names
// must be rejected like any unknown command instead of starting a server.
func TestRemovedSubcommandsAreNotKnownCommands(t *testing.T) {
	for _, name := range []string{"check", "rules"} {
		if knownTopLevelCommand(name) {
			t.Errorf("%q is still accepted as a top-level command and would start the proxy", name)
		}
	}
	for _, name := range []string{"update", "stop", "launcher", "vault", "mcp", "install-chrome", "install-desktop"} {
		if !knownTopLevelCommand(name) {
			t.Errorf("%q must stay a known command", name)
		}
	}
}

type fakeSettings struct {
	value  string
	ok     bool
	getErr error
	sets   map[string]string
}

func (f *fakeSettings) GetSetting(string) (string, bool, error) { return f.value, f.ok, f.getErr }
func (f *fakeSettings) SetSetting(k, v string) error {
	if f.sets == nil {
		f.sets = map[string]string{}
	}
	f.sets[k] = v
	return nil
}

func TestLoadOriginTLSVerifyDoesNotOverwriteOnReadError(t *testing.T) {
	st := &fakeSettings{getErr: errors.New("database is locked")}
	if loadOriginTLSVerify(st) {
		t.Fatal("an unreadable setting must not enable verification by itself")
	}
	if len(st.sets) != 0 {
		t.Fatalf("a failed read must not persist a default over the saved choice, wrote %v", st.sets)
	}
}

func TestLoadOriginTLSVerifyDefaultsAndReads(t *testing.T) {
	missing := &fakeSettings{}
	if loadOriginTLSVerify(missing) || missing.sets["proxy.originTLSVerify"] != "0" {
		t.Fatalf("missing setting: want off and a persisted 0, wrote %v", missing.sets)
	}
	on := &fakeSettings{value: "1", ok: true}
	if !loadOriginTLSVerify(on) || len(on.sets) != 0 {
		t.Fatalf("saved 1 must enable verification without a write, wrote %v", on.sets)
	}
	junk := &fakeSettings{value: "yes", ok: true}
	if loadOriginTLSVerify(junk) || junk.sets["proxy.originTLSVerify"] != "0" {
		t.Fatalf("invalid value: want off and a persisted 0, wrote %v", junk.sets)
	}
}
