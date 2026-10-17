// Command auditdigest recomputes the runtime identity recorded in
// docs/ui-audit/post-release-v2.1.0/manifest.json so a release never needs a
// hand-copied hash. The algorithm mirrors
// TestUIVisualAuditEvidenceMatchesCurrentRuntime in internal/control.
//
//	go run ./tools/auditdigest          # exit 1 if the manifest is stale
//	go run ./tools/auditdigest -write   # rewrite runtime_sha256, runtime_files and audit_harness_sha256
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	manifestRel = "docs/ui-audit/post-release-v2.1.0/manifest.json"
	harnessRel  = "scripts/ui_browser_audit.py"
)

type identity struct {
	RuntimeSHA256 string
	RuntimeFiles  int
	HarnessSHA256 string
}

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the manifest runtime block")
	flag.Parse()
	if err := run(*root, *write); err != nil {
		fmt.Fprintln(os.Stderr, "auditdigest:", err)
		os.Exit(1)
	}
}

func run(root string, write bool) error {
	got, err := compute(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(manifestRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := rewrite(raw, got)
	if err != nil {
		return err
	}
	if bytes.Equal(raw, updated) {
		fmt.Printf("manifest current: %s across %d files\n", got.RuntimeSHA256, got.RuntimeFiles)
		return nil
	}
	if !write {
		return fmt.Errorf("manifest is stale (runtime now %s across %d files); run `go run ./tools/auditdigest -write`", got.RuntimeSHA256, got.RuntimeFiles)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return err
	}
	fmt.Printf("manifest updated: %s across %d files\n", got.RuntimeSHA256, got.RuntimeFiles)
	return nil
}

// compute hashes every non-test runtime source file (tracked, or untracked but
// not ignored) under cmd/, internal/, go.mod and go.sum, plus the audit harness.
func compute(root string) (identity, error) {
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "go.mod", "go.sum", "cmd", "internal")
	cmd.Dir = root
	listed, err := cmd.Output()
	if err != nil {
		return identity{}, fmt.Errorf("enumerate runtime source: %w", err)
	}
	unique := map[string]struct{}{}
	for _, raw := range bytes.Split(listed, []byte{0}) {
		rel := string(raw)
		if rel == "" || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return identity{}, err
		}
		if info.Mode().IsRegular() {
			unique[rel] = struct{}{}
		}
	}
	paths := make([]string, 0, len(unique))
	for rel := range unique {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, rel := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return identity{}, err
		}
		digest.Write([]byte(rel))
		digest.Write([]byte{0})
		sum := sha256.Sum256(content)
		digest.Write(sum[:])
	}
	harness, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(harnessRel)))
	if err != nil {
		return identity{}, err
	}
	hsum := sha256.Sum256(harness)
	return identity{hex.EncodeToString(digest.Sum(nil)), len(paths), hex.EncodeToString(hsum[:])}, nil
}

var (
	reRuntimeSHA = regexp.MustCompile(`("runtime_sha256"\s*:\s*")[0-9a-f]*(")`)
	reFiles      = regexp.MustCompile(`("runtime_files"\s*:\s*)[0-9]+`)
	reHarness    = regexp.MustCompile(`("audit_harness_sha256"\s*:\s*")[0-9a-f]*(")`)
)

// rewrite replaces the three runtime values in place so the rest of the
// manifest keeps its byte-for-byte formatting.
func rewrite(raw []byte, id identity) ([]byte, error) {
	for _, re := range []*regexp.Regexp{reRuntimeSHA, reFiles, reHarness} {
		if !re.Match(raw) {
			return nil, fmt.Errorf("manifest has no field matching %s", re)
		}
	}
	raw = reRuntimeSHA.ReplaceAll(raw, []byte("${1}"+id.RuntimeSHA256+"${2}"))
	raw = reFiles.ReplaceAll(raw, []byte("${1}"+strconv.Itoa(id.RuntimeFiles)))
	raw = reHarness.ReplaceAll(raw, []byte("${1}"+id.HarnessSHA256+"${2}"))
	return raw, nil
}
