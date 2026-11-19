package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/abilities"
)

func writeDirAbility(t *testing.T, dir, id string) {
	t.Helper()
	files := map[string]string{
		"ability.json": `{
  "schema": "interseptor.ability/v1",
  "id": "` + id + `",
  "name": "CLI Test ` + id + `",
  "version": "1.0.0",
  "menus": [{"id": "main", "label": "Main", "group": "abilities", "entry": "ui/panel.js"}],
  "actions": [{"id": "ping", "name": "Ping", "entry": "hooks/action.star", "mcp": true}]
}`,
		"ui/panel.js":       "export function mount(root) { root.textContent = 'ok'; }\n",
		"hooks/action.star": "def run(ctx):\n    return {'ok': True}\n",
	}
	for rel, content := range files {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeZipArchive(t *testing.T, path, id string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"ability.json": `{
  "schema": "interseptor.ability/v1",
  "id": "` + id + `",
  "name": "Zip ` + id + `",
  "version": "0.2.0",
  "menus": [{"id": "main", "label": "Main", "entry": "ui/panel.js"}]
}`,
		"ui/panel.js": "export function mount() {}\n",
	}
	for rel, content := range files {
		hdr := &zip.FileHeader{Name: id + "/" + rel, Method: zip.Deflate}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKnownTopLevelCommandsIncludesAbility(t *testing.T) {
	for _, name := range []string{"ability", "abilities"} {
		if !knownTopLevelCommand(name) {
			t.Errorf("%q must be a known top-level command", name)
		}
	}
}

func TestAbilityCLILifecycle(t *testing.T) {
	dataDir := t.TempDir()
	workDir := t.TempDir()

	runCLI := func(args ...string) (string, error) {
		var out, errBuf bytes.Buffer
		err := runAbilityCLI(args, &out, &errBuf)
		return out.String(), err
	}

	// 1. Empty list.
	out, err := runCLI("list", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if !strings.Contains(out, "No abilities") {
		t.Fatalf("unexpected empty list output: %q", out)
	}

	// 2. Scaffold with interleaved flags.
	out, err = runCLI("scaffold", "header-audit",
		"--data-dir", dataDir,
		"--name", "Header Audit",
		"--group", "recon",
		"--desc", "Audits headers on example.com",
	)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !strings.Contains(out, "header-audit") {
		t.Fatalf("scaffold output = %q", out)
	}

	// 3. List --json verifies quarantined state.
	out, err = runCLI("list", "--json", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var listResp struct {
		Abilities []abilities.Ability `json:"abilities"`
	}
	if err := json.Unmarshal([]byte(out), &listResp); err != nil {
		t.Fatalf("decode list --json: %v (%s)", err, out)
	}
	if len(listResp.Abilities) != 1 || listResp.Abilities[0].ID != "header-audit" ||
		listResp.Abilities[0].Trusted || listResp.Abilities[0].Enabled || !listResp.Abilities[0].Quarantined {
		t.Fatalf("unexpected scaffolded state: %+v", listResp.Abilities)
	}

	// 4. Enable before trust fails.
	if _, err := runCLI("enable", "header-audit", "--data-dir", dataDir); err == nil {
		t.Fatal("expected enable before trust to fail")
	}

	// 5. Trust with --enable=false, then enable, then disable.
	if _, err := runCLI("trust", "header-audit", "--enable=false", "--data-dir", dataDir); err != nil {
		t.Fatalf("trust --enable=false: %v", err)
	}
	out, _ = runCLI("list", "--json", "--data-dir", dataDir)
	_ = json.Unmarshal([]byte(out), &listResp)
	if !listResp.Abilities[0].Trusted || listResp.Abilities[0].Enabled {
		t.Fatalf("expected trusted+disabled: %+v", listResp.Abilities[0])
	}

	if _, err := runCLI("enable", "header-audit", "--data-dir", dataDir); err != nil {
		t.Fatalf("enable: %v", err)
	}
	out, _ = runCLI("list", "--json", "--data-dir", dataDir)
	_ = json.Unmarshal([]byte(out), &listResp)
	if !listResp.Abilities[0].Enabled {
		t.Fatalf("expected enabled: %+v", listResp.Abilities[0])
	}

	if _, err := runCLI("disable", "header-audit", "--data-dir", dataDir); err != nil {
		t.Fatalf("disable: %v", err)
	}
	out, _ = runCLI("list", "--json", "--data-dir", dataDir)
	_ = json.Unmarshal([]byte(out), &listResp)
	if listResp.Abilities[0].Enabled {
		t.Fatalf("expected disabled: %+v", listResp.Abilities[0])
	}

	// 6. Export to .tar.gz and verify archive entries.
	archivePath := filepath.Join(workDir, "header-audit.tar.gz")
	if _, err := runCLI("export", "header-audit", "-o", archivePath, "--data-dir", dataDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	rawArc, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(rawArc))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	foundManifest := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == "header-audit/ability.json" {
			foundManifest = true
		}
	}
	_ = gz.Close()
	if !foundManifest {
		t.Fatal("exported archive missing header-audit/ability.json")
	}

	// 7. Remove and re-install from exported .tar.gz -> starts quarantined!
	if _, err := runCLI("remove", "header-audit", "--data-dir", dataDir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := runCLI("install", archivePath, "--data-dir", dataDir); err != nil {
		t.Fatalf("install tar.gz: %v", err)
	}
	out, _ = runCLI("list", "--json", "--data-dir", dataDir)
	_ = json.Unmarshal([]byte(out), &listResp)
	if len(listResp.Abilities) != 1 || listResp.Abilities[0].Trusted || !listResp.Abilities[0].Quarantined {
		t.Fatalf("reinstalled tar.gz must start quarantined: %+v", listResp.Abilities)
	}

	// 8. Install from a local directory into a project scope (--project + --scope project).
	localDir := filepath.Join(workDir, "dir-ability-src")
	writeDirAbility(t, localDir, "dir-ability")
	if _, err := runCLI("install", localDir, "--data-dir", dataDir, "--project", "acme", "--scope", "project"); err != nil {
		t.Fatalf("install dir: %v", err)
	}

	// 9. Install from a .zip into project scope.
	zipPath := filepath.Join(workDir, "zip-ability.zip")
	writeZipArchive(t, zipPath, "zip-ability")
	if _, err := runCLI("install", zipPath, "--data-dir", dataDir, "--project", "acme"); err != nil {
		t.Fatalf("install zip: %v", err)
	}

	// Tabular list with --project shows global + project abilities, all quarantined.
	out, err = runCLI("list", "--data-dir", dataDir, "--project", "acme")
	if err != nil {
		t.Fatalf("tabular list: %v", err)
	}
	for _, want := range []string{"header-audit", "dir-ability", "zip-ability", "quarantined"} {
		if !strings.Contains(out, want) {
			t.Errorf("tabular list missing %q:\n%s", want, out)
		}
	}

	// 10. Trust default (--enable=true) on project ability.
	if _, err := runCLI("trust", "dir-ability", "--data-dir", dataDir, "--project", "acme"); err != nil {
		t.Fatalf("trust default: %v", err)
	}
	out, _ = runCLI("list", "--json", "--data-dir", dataDir, "--project", "acme")
	_ = json.Unmarshal([]byte(out), &listResp)
	var found bool
	for _, a := range listResp.Abilities {
		if a.ID == "dir-ability" {
			found = true
			if !a.Trusted || !a.Enabled || a.Scope != abilities.ScopeProject {
				t.Fatalf("dir-ability after trust = %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("dir-ability missing from project list")
	}

	// 11. Error cases: --scope project without --project, --project default with --scope project, help, unknown subcommand.
	if _, err := runCLI("install", zipPath, "--data-dir", dataDir, "--scope", "project"); err == nil {
		t.Fatal("expected error for --scope project without --project")
	}
	if _, err := runCLI("install", zipPath, "--data-dir", dataDir, "--project", "default", "--scope", "project"); err == nil {
		t.Fatal("expected error for --project default with --scope project")
	}
	if out, err := runCLI("scaffold", "default-proj-tool", "--data-dir", dataDir, "--project", "default"); err != nil || !strings.Contains(out, "scope=global") {
		t.Fatalf("scaffold on --project default should fall back to global: out=%q err=%v", out, err)
	}
	if _, err := runCLI("help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	if _, err := runCLI("bogus"); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}
