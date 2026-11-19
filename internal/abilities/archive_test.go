package abilities

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func buildTarGz(t *testing.T, entries []struct {
	name     string
	body     string
	typeflag byte
	linkname string
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			Typeflag: tf,
			Linkname: e.linkname,
		}
		if tf == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, entries []struct {
	name string
	body string
	mode os.FileMode
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:   e.name,
			Method: zip.Deflate,
		}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validManifestJSON(id string) string {
	return `{
		"schema": "interseptor.ability/v1",
		"id": "` + id + `",
		"name": "Ability ` + id + `",
		"version": "1.2.3",
		"menus": [{"id": "main", "label": "Main", "entry": "ui/panel.js"}],
		"hooks": [{"event": "flow.captured", "entry": "hooks/on_flow.star"}],
		"actions": [{"id": "run-me", "name": "Run", "entry": "hooks/action.star"}]
	}`
}

func TestInstallArchiveTarGzAndZip(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()

	var changes atomic.Int32
	mgr, err := NewManager(Config{
		GlobalDir:  globalDir,
		ProjectDir: projectDir,
		OnChange:   func() { changes.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Wrapped tar.gz with ./ prefix
	tgz := buildTarGz(t, []struct {
		name     string
		body     string
		typeflag byte
		linkname string
	}{
		{name: "./wrapped-dir/", typeflag: tar.TypeDir},
		{name: "./wrapped-dir/ability.json", body: validManifestJSON("tar-tool")},
		{name: "./wrapped-dir/ui/panel.js", body: "export function mount() {}\n"},
		{name: "./wrapped-dir/hooks/on_flow.star", body: "def on_flow(flow):\n    return None\n"},
		{name: "./wrapped-dir/hooks/action.star", body: "def run(ctx):\n    return {'ok': True}\n"},
	})

	a1, err := mgr.InstallArchive(bytes.NewReader(tgz), 4<<20, ScopeProject)
	if err != nil {
		t.Fatalf("InstallArchive tar.gz: %v", err)
	}
	if a1.ID != "tar-tool" || a1.Scope != ScopeProject {
		t.Fatalf("unexpected ability: %+v", a1)
	}
	if a1.Trusted || a1.Enabled || !a1.Quarantined {
		t.Fatalf("installed tar ability must start quarantined: %+v", a1)
	}

	// 2. Flat zip into global scope
	zp := buildZip(t, []struct {
		name string
		body string
		mode os.FileMode
	}{
		{name: "ability.json", body: validManifestJSON("zip-tool")},
		{name: "ui/panel.js", body: "export function mount() {}\n"},
		{name: "hooks/on_flow.star", body: "def on_flow(flow):\n    return None\n"},
		{name: "hooks/action.star", body: "def run(ctx):\n    return {'ok': True}\n"},
	})

	a2, err := mgr.InstallArchive(bytes.NewReader(zp), 4<<20, ScopeGlobal)
	if err != nil {
		t.Fatalf("InstallArchive zip: %v", err)
	}
	if a2.ID != "zip-tool" || a2.Scope != ScopeGlobal {
		t.Fatalf("unexpected zip ability: %+v", a2)
	}
	if a2.Trusted || a2.Enabled || !a2.Quarantined {
		t.Fatalf("installed zip ability must start quarantined: %+v", a2)
	}

	// Trust tar-tool, then re-install over it -> must revert to quarantined!
	if _, err := mgr.TrustAndEnable("tar-tool", true); err != nil {
		t.Fatal(err)
	}
	reinstalled, err := mgr.InstallArchive(bytes.NewReader(tgz), 4<<20, ScopeProject)
	if err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if reinstalled.Trusted || reinstalled.Enabled || !reinstalled.Quarantined {
		t.Fatalf("re-installed ability must be quarantined: %+v", reinstalled)
	}
	if changes.Load() != 4 {
		t.Fatalf("OnChange count = %d want 4", changes.Load())
	}
}

func TestInstallArchiveSecurityRejections(t *testing.T) {
	mgr, err := NewManager(Config{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bad magic bytes", func(t *testing.T) {
		if _, err := mgr.InstallArchive(bytes.NewBufferString("not an archive"), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected error for invalid archive magic")
		}
	})

	t.Run("exceeds maxBytes", func(t *testing.T) {
		zp := buildZip(t, []struct {
			name string
			body string
			mode os.FileMode
		}{
			{name: "ability.json", body: validManifestJSON("too-big")},
			{name: "ui/panel.js", body: "export function mount() {}\n"},
			{name: "hooks/on_flow.star", body: "def on_flow(flow):\n    return None\n"},
			{name: "hooks/action.star", body: "def run(ctx):\n    return {}\n"},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(zp), 50, ScopeProject); err == nil {
			t.Fatal("expected error when archive exceeds maxBytes")
		}
	})

	t.Run("tar traversal", func(t *testing.T) {
		tgz := buildTarGz(t, []struct {
			name     string
			body     string
			typeflag byte
			linkname string
		}{
			{name: "../escape.txt", body: "x"},
			{name: "ability.json", body: validManifestJSON("trav")},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(tgz), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected path traversal rejection")
		}
	})

	t.Run("tar symlink", func(t *testing.T) {
		tgz := buildTarGz(t, []struct {
			name     string
			body     string
			typeflag byte
			linkname string
		}{
			{name: "ability.json", body: validManifestJSON("sym")},
			{name: "ui/panel.js", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
			{name: "hooks/on_flow.star", body: "def on_flow(flow):\n    return None\n"},
			{name: "hooks/action.star", body: "def run(ctx):\n    return {}\n"},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(tgz), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected tar symlink rejection")
		}
	})

	t.Run("tar hardlink", func(t *testing.T) {
		tgz := buildTarGz(t, []struct {
			name     string
			body     string
			typeflag byte
			linkname string
		}{
			{name: "ability.json", body: validManifestJSON("hl")},
			{name: "ui/panel.js", typeflag: tar.TypeLink, linkname: "/etc/passwd"},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(tgz), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected tar hardlink rejection")
		}
	})

	t.Run("tar fifo special file", func(t *testing.T) {
		tgz := buildTarGz(t, []struct {
			name     string
			body     string
			typeflag byte
			linkname string
		}{
			{name: "ability.json", body: validManifestJSON("fifo")},
			{name: "ui/pipe", typeflag: tar.TypeFifo},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(tgz), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected tar special file rejection")
		}
	})

	t.Run("zip traversal", func(t *testing.T) {
		zp := buildZip(t, []struct {
			name string
			body string
			mode os.FileMode
		}{
			{name: "../../evil.txt", body: "bad"},
			{name: "ability.json", body: validManifestJSON("ztrav")},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(zp), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected zip traversal rejection")
		}
	})

	t.Run("zip symlink", func(t *testing.T) {
		zp := buildZip(t, []struct {
			name string
			body string
			mode os.FileMode
		}{
			{name: "ability.json", body: validManifestJSON("zsym")},
			{name: "ui/panel.js", body: "/etc/passwd", mode: os.ModeSymlink | 0o777},
			{name: "hooks/on_flow.star", body: "def on_flow(flow):\n    return None\n"},
			{name: "hooks/action.star", body: "def run(ctx):\n    return {}\n"},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(zp), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected zip symlink rejection")
		}
	})

	t.Run("invalid starlark in hook", func(t *testing.T) {
		zp := buildZip(t, []struct {
			name string
			body string
			mode os.FileMode
		}{
			{name: "ability.json", body: validManifestJSON("bad-star")},
			{name: "ui/panel.js", body: "export function mount() {}\n"},
			{name: "hooks/on_flow.star", body: "def wrong_name(flow):\n    return None\n"},
			{name: "hooks/action.star", body: "def run(ctx):\n    return {}\n"},
		})
		if _, err := mgr.InstallArchive(bytes.NewReader(zp), 1<<20, ScopeProject); err == nil {
			t.Fatal("expected compile/entrypoint error for missing on_flow")
		}
	})
}

func TestScaffoldAndExportDeterminism(t *testing.T) {
	srcProject := t.TempDir()
	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: srcProject})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ab, err := mgr.Scaffold(ScaffoldRequest{
		ID:          "header-helper",
		Name:        "Header Helper",
		Description: "Inspects headers on example.com",
		Scope:       ScopeProject,
		Group:       "recon",
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if ab.Trusted || ab.Enabled || !ab.Quarantined {
		t.Fatalf("scaffolded ability must start quarantined: %+v", ab)
	}
	if len(ab.Manifest.Menus) != 1 || ab.Manifest.Menus[0].Group != "recon" {
		t.Fatalf("unexpected menus: %+v", ab.Manifest.Menus)
	}
	for _, rel := range []string{
		"ability.json",
		"ui/panel.js",
		"ui/style.css",
		"ui/inspector.js",
		"hooks/on_flow.star",
		"hooks/action.star",
	} {
		if _, err := os.Stat(filepath.Join(ab.Dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing scaffolded file %s: %v", rel, err)
		}
	}

	// Duplicate scaffold fails
	if _, err := mgr.Scaffold(ScaffoldRequest{ID: "header-helper", Name: "Dup", Scope: ScopeProject}); err == nil {
		t.Fatal("expected duplicate scaffold to fail")
	}

	// Export twice -> byte-for-byte deterministic
	var exp1, exp2 bytes.Buffer
	if err := mgr.ExportArchive("header-helper", &exp1); err != nil {
		t.Fatalf("ExportArchive 1: %v", err)
	}
	if err := mgr.ExportArchive("header-helper", &exp2); err != nil {
		t.Fatalf("ExportArchive 2: %v", err)
	}
	if !bytes.Equal(exp1.Bytes(), exp2.Bytes()) {
		t.Fatal("ExportArchive output is not deterministic")
	}

	// Round-trip into fresh manager
	dstMgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer dstMgr.Close()
	imported, err := dstMgr.InstallArchive(bytes.NewReader(exp1.Bytes()), 4<<20, ScopeProject)
	if err != nil {
		t.Fatalf("round-trip InstallArchive: %v", err)
	}
	if imported.Digest != ab.Digest {
		t.Fatalf("round-trip digest mismatch: %s != %s", imported.Digest, ab.Digest)
	}

	// Trust & execute scaffolded action
	if _, err := dstMgr.TrustAndEnable("header-helper", true); err != nil {
		t.Fatal(err)
	}
	res, err := dstMgr.RunAction(nil, "header-helper", "inspect", map[string]any{"target": "https://example.com"})
	if err != nil {
		t.Fatalf("RunAction on scaffolded ability: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %+v", res)
	}
}
