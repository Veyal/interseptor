package abilities

import (
	"bytes"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func createSampleAbility(t *testing.T, baseDir, id, version string) string {
	t.Helper()
	dir := filepath.Join(baseDir, "abilities", id)
	writeTestAbilityFiles(t, dir, map[string]string{
		"ability.json": `{
			"schema": "interseptor.ability/v1",
			"id": "` + id + `",
			"name": "Ability ` + id + `",
			"version": "` + version + `",
			"menus": [{"id": "main", "label": "Main", "entry": "ui/panel.js", "style": "ui/style.css"}],
			"hooks": [{"event": "flow.captured", "entry": "hooks/on_flow.star"}],
			"actions": [{"id": "ping", "name": "Ping", "entry": "hooks/action.star", "mcp": true}]
		}`,
		"ui/panel.js":        "export function mount(el) { el.textContent = 'hi'; }\n",
		"ui/style.css":       ".a { color: red; }\n",
		"ui/logo.svg":        "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>\n",
		"ui/data.json":       `{"host":"example.com"}`,
		"ui/img.png":         "pngdata",
		"ui/img.webp":        "webpdata",
		"ui/readme.txt":      "hello",
		"hooks/on_flow.star": "def on_flow(flow):\n    return None\n",
		"hooks/action.star":  "def run(ctx):\n    return {'pong': True}\n",
	})
	return dir
}

func TestManagerDiscoveryQuarantineTrustAndDrift(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	createSampleAbility(t, globalDir, "shared-tool", "1.0.0")
	createSampleAbility(t, globalDir, "global-only", "1.0.0")
	createSampleAbility(t, projectDir, "shared-tool", "2.0.0")

	// Also drop a hidden temp dir (must be skipped) and a broken ability (must surface LoadError)
	if err := os.MkdirAll(filepath.Join(projectDir, "abilities", ".stage-123"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAbilityFiles(t, filepath.Join(projectDir, "abilities", "broken-one"), map[string]string{
		"ability.json": `{not valid json`,
	})

	var changeCalls atomic.Int32
	var mgr *Manager
	mgr, err := NewManager(Config{
		GlobalDir:  globalDir,
		ProjectDir: projectDir,
		OnChange: func() {
			changeCalls.Add(1)
			// Verify OnChange is invoked outside the lock: List() must not deadlock.
			_ = mgr.List()
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	list := mgr.List()
	if len(list) != 3 {
		t.Fatalf("len(List) = %d, want 3 (%+v)", len(list), list)
	}

	// Project overrides global for shared-tool
	shared, ok := mgr.Get("shared-tool")
	if !ok {
		t.Fatal("shared-tool not found")
	}
	if shared.Scope != ScopeProject || shared.Manifest.Version != "2.0.0" {
		t.Fatalf("shared-tool = scope %q version %q, want project 2.0.0", shared.Scope, shared.Manifest.Version)
	}
	if shared.Trusted || shared.Enabled || !shared.Quarantined {
		t.Fatalf("newly discovered ability must start quarantined: %+v", shared)
	}

	// Cannot enable untrusted ability
	if _, err := mgr.SetEnabled("shared-tool", true); err == nil {
		t.Fatal("expected SetEnabled on untrusted ability to fail")
	}

	// Broken ability has LoadError and cannot be enabled or trusted
	broken, ok := mgr.Get("broken-one")
	if !ok || broken.LoadError == "" {
		t.Fatalf("expected broken-one with LoadError, got %+v (ok=%v)", broken, ok)
	}
	if _, err := mgr.SetEnabled("broken-one", true); err == nil {
		t.Fatal("expected SetEnabled on broken ability to fail")
	}
	if _, err := mgr.TrustAndEnable("broken-one", true); err == nil {
		t.Fatal("expected TrustAndEnable on broken ability to fail")
	}

	// Trust & enable shared-tool
	trusted, err := mgr.TrustAndEnable("shared-tool", true)
	if err != nil {
		t.Fatalf("TrustAndEnable: %v", err)
	}
	if !trusted.Trusted || !trusted.Enabled || trusted.Quarantined || trusted.Drift {
		t.Fatalf("unexpected state after TrustAndEnable: %+v", trusted)
	}
	if changeCalls.Load() != 1 {
		t.Fatalf("OnChange calls = %d, want 1", changeCalls.Load())
	}

	// Per-scope state persistence: re-create Manager and ensure shared-tool stays trusted,
	// and global-only stays quarantined!
	mgr2, err := NewManager(Config{GlobalDir: globalDir, ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	s2, _ := mgr2.Get("shared-tool")
	if !s2.Trusted || !s2.Enabled {
		t.Fatalf("shared-tool should remain trusted+enabled across reload: %+v", s2)
	}
	g2, _ := mgr2.Get("global-only")
	if g2.Trusted || g2.Enabled {
		t.Fatalf("global-only should still be quarantined: %+v", g2)
	}

	// Simulate file tampering on disk -> Drift re-quarantine!
	if err := os.WriteFile(filepath.Join(shared.Dir, "ui", "panel.js"), []byte("export function mount(){/*tampered*/}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr2.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	drifted, _ := mgr2.Get("shared-tool")
	if !drifted.Drift || drifted.Trusted || drifted.Enabled || !drifted.Quarantined {
		t.Fatalf("expected drift re-quarantine, got %+v", drifted)
	}
	if drifted.TrustError != "ability files changed on disk since approval" {
		t.Fatalf("TrustError = %q", drifted.TrustError)
	}
	if _, err := mgr2.SetEnabled("shared-tool", true); err == nil {
		t.Fatal("expected SetEnabled to fail on drifted ability")
	}

	// Trusting again approves the new digest
	retrusted, err := mgr2.TrustAndEnable("shared-tool", false)
	if err != nil {
		t.Fatalf("re-TrustAndEnable: %v", err)
	}
	if !retrusted.Trusted || retrusted.Enabled || retrusted.Drift {
		t.Fatalf("expected trusted & disabled after TrustAndEnable(false): %+v", retrusted)
	}
	enabled, err := mgr2.SetEnabled("shared-tool", true)
	if err != nil || !enabled.Enabled {
		t.Fatalf("SetEnabled(true): err=%v enabled=%+v", err, enabled)
	}
}

func TestManagerSameGlobalAndProjectDir(t *testing.T) {
	dir := t.TempDir()
	createSampleAbility(t, dir, "default-proj-ability", "1.0.0")

	mgr, err := NewManager(Config{GlobalDir: dir, ProjectDir: dir})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("len(List) = %d, want 1", len(list))
	}
	if list[0].OverridesGlobal {
		t.Fatal("same directory must not mark OverridesGlobal=true")
	}
}

func TestManagerReadAssetGatingRevisionAndContentTypes(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "asset-test", "1.0.0")

	// Also drop a symlink escape inside ui/
	outside := filepath.Join(t.TempDir(), "secret.js")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(abDir, "ui", "escape.js")); err != nil {
		t.Fatal(err)
	}
	// Remove the unreferenced escape before validation, or test on a valid ability first:
	_ = os.Remove(filepath.Join(abDir, "ui", "escape.js"))

	mgr, err := NewManager(Config{GlobalDir: globalDir, ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	quar, _ := mgr.Get("asset-test")
	// Must fail when quarantined
	if _, _, ok := mgr.ReadAsset("asset-test", "ui/panel.js", quar.Digest); ok {
		t.Fatal("ReadAsset succeeded while quarantined")
	}

	trusted, err := mgr.TrustAndEnable("asset-test", false)
	if err != nil {
		t.Fatal(err)
	}
	// Must fail when trusted but disabled
	if _, _, ok := mgr.ReadAsset("asset-test", "ui/panel.js", trusted.Digest); ok {
		t.Fatal("ReadAsset succeeded while disabled")
	}

	enabled, err := mgr.SetEnabled("asset-test", true)
	if err != nil {
		t.Fatal(err)
	}
	rev := enabled.Digest

	// Wrong or missing revision must be rejected.
	if _, _, ok := mgr.ReadAsset("asset-test", "ui/panel.js", ""); ok {
		t.Fatal("ReadAsset succeeded with empty revision")
	}
	if _, _, ok := mgr.ReadAsset("asset-test", "ui/panel.js", "sha256:0000"); ok {
		t.Fatal("ReadAsset succeeded with mismatched revision")
	}

	// Never serve hooks/, ability.json, or traversal paths
	for _, blocked := range []string{
		"ability.json",
		"hooks/on_flow.star",
		"hooks/action.star",
		"ui/../ability.json",
		"/ui/panel.js",
		"ui/missing.js",
		"",
	} {
		if data, _, ok := mgr.ReadAsset("asset-test", blocked, rev); ok {
			t.Fatalf("ReadAsset(%q) unexpectedly succeeded (%d bytes)", blocked, len(data))
		}
	}

	// Allowed assets & content-types
	wantTypes := map[string]string{
		"ui/panel.js":   "text/javascript; charset=utf-8",
		"ui/style.css":  "text/css; charset=utf-8",
		"ui/logo.svg":   "image/svg+xml",
		"ui/data.json":  "application/json",
		"ui/img.png":    "image/png",
		"ui/img.webp":   "image/webp",
		"ui/readme.txt": "text/plain; charset=utf-8",
	}
	for rel, wantCT := range wantTypes {
		data, ct, ok := mgr.ReadAsset("asset-test", rel, rev)
		if !ok {
			t.Fatalf("ReadAsset(%q) failed", rel)
		}
		if ct != wantCT {
			t.Fatalf("ReadAsset(%q) ct = %q want %q", rel, ct, wantCT)
		}
		if len(data) == 0 {
			t.Fatalf("ReadAsset(%q) returned empty bytes", rel)
		}
	}

	// Disk tamper before Reload must not alter bytes served for the approved revision.
	origPanel := []byte("export function mount(el) { el.textContent = 'hi'; }\n")
	if err := os.WriteFile(filepath.Join(abDir, "ui", "panel.js"), []byte("export function mount(){alert('tampered')}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _, ok := mgr.ReadAsset("asset-test", "ui/panel.js", rev)
	if !ok || !bytes.Equal(data, origPanel) {
		t.Fatalf("ReadAsset served tampered disk bytes before reload: ok=%v data=%q", ok, data)
	}

	// Symlink dropped after enabling must still be rejected by ReadAsset
	if err := os.Symlink(outside, filepath.Join(abDir, "ui", "escape.js")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := mgr.ReadAsset("asset-test", "ui/escape.js", rev); ok {
		t.Fatal("ReadAsset allowed symlink escape")
	}
}

func TestManagerRemove(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "to-remove", "1.0.0")

	var changes atomic.Int32
	var mgr *Manager
	mgr, err := NewManager(Config{
		GlobalDir:  globalDir,
		ProjectDir: projectDir,
		OnChange: func() {
			changes.Add(1)
			_ = mgr.List()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if _, err := mgr.TrustAndEnable("to-remove", true); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Remove("to-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(abDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be deleted", abDir)
	}
	if _, ok := mgr.Get("to-remove"); ok {
		t.Fatal("expected ability to disappear from manager after Remove")
	}
	if changes.Load() != 2 {
		t.Fatalf("OnChange calls = %d want 2", changes.Load())
	}

	// Re-adding same ID must start quarantined (trust entry was removed)
	createSampleAbility(t, projectDir, "to-remove", "1.0.0")
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	reloaded, ok := mgr.Get("to-remove")
	if !ok || reloaded.Trusted || reloaded.Enabled {
		t.Fatalf("re-added ability must start quarantined: %+v", reloaded)
	}
}
