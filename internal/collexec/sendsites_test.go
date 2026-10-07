package collexec

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The scope policy and dial-time guard live in the Step pipeline, so a second
// way onto the network would silently skip them. This test pins the send sites
// of the whole collections stack: only Step (step.go) and the auth Doer
// (auth.go) may call the sender, nothing may dial or use a bare HTTP client,
// and every production collauth.Manager is built with the guarded StepDoer.
// (The behavioural counterpart, which drives each path end to end, is
// TestEverySendPathAppliesTheSameScopeAndDialGuard in internal/control.)

var collectionStackDirs = []string{
	"internal/collexec", "internal/collrun", "internal/collauth", "internal/collreport", "internal/collimport",
	"internal/collexport", "internal/collection", "internal/pmsandbox", "internal/scriptworker", "internal/scriptctx",
	"internal/jsrt", "internal/varstore",
	// internal/mcp is deliberately absent: its tools reach collections only
	// through the control REST API, whose handlers use the same backend.
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for ; dir != "/"; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	t.Fatal("go.mod not found")
	return ""
}

func productionFiles(t *testing.T, root string, dirs []string, extra ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(b)
	}
	for _, d := range dirs {
		_ = filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !strings.Contains(p, "/testdata/") {
				add(p)
			}
			return nil
		})
	}
	for _, f := range extra {
		add(filepath.Join(root, f))
	}
	return out
}

func TestCollectionStackHasOnlyGuardedSendSites(t *testing.T) {
	root := repoRoot(t)
	files := productionFiles(t, root, collectionStackDirs,
		"cmd/interseptor/run_cli.go", "internal/control/collections.go", "internal/control/collections_run.go",
		"internal/control/collections_runner.go", "internal/control/collections_oauth.go", "internal/control/collections_backend.go")

	senderCall := regexp.MustCompile(`\bSender\.Send\(|\bsnd\.Send\(`)
	senderBuild := regexp.MustCompile(`\bsender\.New\(`)
	network := regexp.MustCompile(`http\.DefaultClient|http\.Get\(|http\.Post\(|http\.PostForm\(|http\.Head\(|net\.Dial|\.DialContext\(|http\.Transport\{|&http\.Client\{`)
	allowedSend := map[string]bool{
		"internal/collexec/step.go": true, "internal/collexec/auth.go": true,
		// These call the injected scriptctx.SendFunc (pm.sendRequest), which every host
		// binds to a nested Step with scripts disabled; they hold no sender of their own.
		"internal/pmsandbox/host.go": true, "internal/scriptworker/exec.go": true,
	}
	// The headless CLI builds the process-wide egress sender once and hands it
	// to the backend; nothing else in the stack may build one.
	allowedBuild := map[string]bool{"cmd/interseptor/run_cli.go": true}
	// collauth keeps a plain client only as the documented fallback when no
	// Doer is injected; production constructors must pass one (checked below).
	allowedNetwork := map[string]bool{"internal/collauth/auth.go": true}

	for path, src := range files {
		if senderCall.MatchString(src) && !allowedSend[path] {
			t.Errorf("%s sends or builds a sender outside the Step pipeline; route it through collexec.Pipeline.Step", path)
		}
		if senderBuild.MatchString(src) && !allowedBuild[path] {
			t.Errorf("%s builds its own sender; collection traffic must use the backend's sender through Step", path)
		}
		if network.MatchString(src) && !allowedNetwork[path] {
			t.Errorf("%s opens network access directly (%s); collection traffic must go through the sender", path, network.FindString(src))
		}
	}

	// Every production collauth.Manager is constructed with the guarded doer.
	all := productionFiles(t, root, []string{"internal", "cmd"})
	ctor := regexp.MustCompile(`collauth\.New\(([^)]*)\)`)
	for path, src := range all {
		if strings.HasPrefix(path, "internal/collauth/") {
			continue
		}
		for _, m := range ctor.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(m[1], "Doer:") || !strings.Contains(m[1], "StepDoer") {
				t.Errorf("%s builds a collauth.Manager without collexec.StepDoer: token requests would bypass the scope and dial guard", path)
			}
		}
	}
}
