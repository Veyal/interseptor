package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteReplacesOnlyRuntimeValues(t *testing.T) {
	in := "{\n  \"evidence\": [{\"sha256\": \"keep\"}],\n  \"runtime\": {\n    \"audit_harness_sha256\": \"aa\",\n    \"runtime_files\": 3,\n    \"runtime_sha256\": \"bb\"\n  }\n}\n"
	out, err := rewrite([]byte(in), identity{RuntimeSHA256: "cc", RuntimeFiles: 7, HarnessSHA256: "dd"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(`"aa"`, `"dd"`, `: 3`, `: 7`, `"bb"`, `"cc"`).Replace(in)
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestRewriteRejectsManifestWithoutRuntimeBlock(t *testing.T) {
	if _, err := rewrite([]byte("{}"), identity{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunDetectsStaleThenWritesThenCurrent(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	put := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("go.mod", "module example.com/x\n")
	put("internal/a/a.go", "package a\n")
	put("internal/a/a_test.go", "package a\n")
	put("scripts/ui_browser_audit.py", "print(1)\n")
	put(manifestRel, "{\n  \"runtime\": {\n    \"audit_harness_sha256\": \"\",\n    \"runtime_files\": 0,\n    \"runtime_sha256\": \"\"\n  }\n}\n")

	if err := run(root, false); err == nil {
		t.Fatal("stale manifest must fail without -write")
	}
	if err := run(root, true); err != nil {
		t.Fatal(err)
	}
	if err := run(root, false); err != nil {
		t.Fatalf("manifest should be current after -write: %v", err)
	}
	id, _ := compute(root)
	if id.RuntimeFiles != 2 { // go.mod + a.go; the _test.go file is excluded
		t.Fatalf("files = %d, want 2", id.RuntimeFiles)
	}
	put("internal/a/a.go", "package a // changed\n")
	if err := run(root, false); err == nil {
		t.Fatal("source change must make the manifest stale")
	}
}
