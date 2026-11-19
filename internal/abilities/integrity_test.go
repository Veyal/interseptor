package abilities

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/plugin"
	"github.com/Veyal/interseptor/internal/store"
)

func TestProjectTrustIgnoresProjectCarriedState(t *testing.T) {
	globalDir, projectDir := t.TempDir(), t.TempDir()
	const id = "local-tool"
	dir := createSampleAbility(t, projectDir, id, "1")
	digest, err := ComputeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveTrustStates(projectDir, map[string]TrustState{id: {TrustedDigest: digest, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(Config{GlobalDir: globalDir, ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if ab, _ := mgr.Get(id); !ab.Quarantined || ab.Trusted || ab.Enabled {
		t.Fatalf("project-carried approval was honored: %+v", ab)
	}
	if _, err := mgr.TrustAndEnable(id, true); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(projectDir, alias); err != nil {
		t.Fatal(err)
	}
	mgr, err = NewManager(Config{GlobalDir: globalDir, ProjectDir: alias})
	if err != nil {
		t.Fatal(err)
	}
	if ab, _ := mgr.Get(id); !ab.Trusted || !ab.Enabled {
		t.Fatalf("canonical project alias lost local approval: %+v", ab)
	}
	other := t.TempDir()
	createSampleAbility(t, other, id, "1")
	mgr, err = NewManager(Config{GlobalDir: globalDir, ProjectDir: other})
	if err != nil {
		t.Fatal(err)
	}
	if ab, _ := mgr.Get(id); ab.Trusted || ab.Enabled {
		t.Fatalf("local approval leaked to a different project: %+v", ab)
	}
}

func TestQuarantinedScriptIsNotInitialized(t *testing.T) {
	globalDir := t.TempDir()
	const id = "inert-tool"
	dir := createSampleAbility(t, globalDir, id, "1")
	writeTestAbilityFiles(t, dir, map[string]string{
		"hooks/action.star": "fail('initialization was called')\ndef run(ctx):\n    return None\n",
	})
	mgr, err := NewManager(Config{GlobalDir: globalDir})
	if err != nil {
		t.Fatal(err)
	}
	if ab, _ := mgr.Get(id); ab.LoadError != "" || !ab.Quarantined {
		t.Fatalf("quarantined action was initialized: %+v", ab)
	}
	ab, err := mgr.TrustAndEnable(id, false)
	if err != nil || !ab.Trusted || ab.Enabled || ab.LoadError != "" {
		t.Fatalf("approving without enabling should stay inert: ab=%+v err=%v", ab, err)
	}
	if ab, err := mgr.SetEnabled(id, true); err == nil && (ab.Enabled || ab.LoadError == "") {
		t.Fatalf("top-level failure did not block activation: %+v", ab)
	}
}

func TestHiddenReferencedFileIsNotPartOfAnAbility(t *testing.T) {
	dir := t.TempDir()
	writeTestAbilityFiles(t, dir, map[string]string{
		"ability.json":          `{"schema":"interseptor.ability/v1","id":"hidden-entry","name":"Hidden","version":"1","actions":[{"id":"ping","name":"Ping","entry":"hooks/.entry/run.star"}]}`,
		"hooks/.entry/run.star": "def run(ctx):\n    return None\n",
	})
	if _, _, err := ValidateDir(dir); err == nil {
		t.Fatal("accepted manifest entry excluded from the content digest")
	}
}

func TestProjectDiscoveryWithoutGlobalDirStaysQuarantinedAndFailsClosed(t *testing.T) {
	projectDir := t.TempDir()
	createSampleAbility(t, projectDir, "proj-tool", "1.0.0")
	mgr, err := NewManager(Config{ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ab, ok := mgr.Get("proj-tool")
	if !ok || !ab.Quarantined || ab.Trusted {
		t.Fatalf("expected quarantined discovery without GlobalDir, got %+v", ab)
	}
	if _, err := mgr.TrustAndEnable("proj-tool", true); err == nil {
		t.Fatal("expected TrustAndEnable to fail closed without GlobalDir")
	}
}

func TestAliasedDefaultProjectRejectsProjectScope(t *testing.T) {
	shared := t.TempDir()
	mgr, err := NewManager(Config{GlobalDir: shared, ProjectDir: shared})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if mgr.HasProjectScope() {
		t.Fatal("HasProjectScope must be false when ProjectDir aliases GlobalDir")
	}
	if _, err := mgr.Scaffold(ScaffoldRequest{ID: "aliased-proj", Name: "Aliased", Scope: ScopeProject}); err == nil {
		t.Fatal("expected Scaffold with ScopeProject to fail when ProjectDir aliases GlobalDir")
	}
	ab, err := mgr.Scaffold(ScaffoldRequest{ID: "aliased-default", Name: "Default"})
	if err != nil {
		t.Fatalf("Scaffold with empty scope fell back to global: %v", err)
	}
	if ab.Scope != ScopeGlobal {
		t.Fatalf("expected fallback scope global, got %q", ab.Scope)
	}
}

func TestShadowedGlobalInstallReturnsGlobalQuarantinedEntry(t *testing.T) {
	globalDir, projectDir := t.TempDir(), t.TempDir()
	createSampleAbility(t, projectDir, "shadowed-id", "2.0.0")
	mgr, err := NewManager(Config{GlobalDir: globalDir, ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if _, err := mgr.TrustAndEnable("shadowed-id", true); err != nil {
		t.Fatal(err)
	}

	tgz := buildTarGz(t, []struct {
		name     string
		body     string
		typeflag byte
		linkname string
	}{
		{name: "shadowed-id/ability.json", body: `{"schema":"interseptor.ability/v1","id":"shadowed-id","name":"Global Copy","version":"1.0.0","menus":[{"id":"main","label":"Main","entry":"ui/panel.js"}]}`},
		{name: "shadowed-id/ui/panel.js", body: "export function mount() {}\n"},
	})
	installed, err := mgr.InstallArchive(bytes.NewReader(tgz), 1<<20, ScopeGlobal)
	if err != nil {
		t.Fatalf("InstallArchive global: %v", err)
	}
	if installed.Scope != ScopeGlobal || !installed.Quarantined || installed.Trusted {
		t.Fatalf("global install returned shadowed/trusted entry: %+v", installed)
	}
}

func TestSymlinkedAbilityDirIsRejectedAndNeverFollowed(t *testing.T) {
	globalDir := t.TempDir()
	outside := t.TempDir()
	writeTestAbilityFiles(t, outside, map[string]string{
		"ability.json": `{"schema":"interseptor.ability/v1","id":"sym-dir","name":"Sym","version":"1.0.0"}`,
	})
	abilitiesRoot := filepath.Join(globalDir, "abilities")
	if err := os.MkdirAll(abilitiesRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(abilitiesRoot, "sym-dir")); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(Config{GlobalDir: globalDir})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ab, ok := mgr.Get("sym-dir")
	if !ok || ab.LoadError == "" || !ab.Quarantined {
		t.Fatalf("expected quarantined LoadError for symlinked ability dir, got %+v (ok=%v)", ab, ok)
	}
	if _, err := mgr.TrustAndEnable("sym-dir", true); err == nil {
		t.Fatal("expected TrustAndEnable to reject symlinked ability dir")
	}
}

func TestTarBombHiddenSkippedPayloadIsRejected(t *testing.T) {
	mgr, err := NewManager(Config{GlobalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Build a tar.gz containing many 512 KiB hidden files that individually sit below MaxFileBytes
	// but together exceed maxTarStreamBytes even when hidden entries are skipped.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := `{"schema":"interseptor.ability/v1","id":"hidden-bomb","name":"Bomb","version":"1.0.0"}`
	_ = tw.WriteHeader(&tar.Header{Name: "ability.json", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(manifest))
	chunk := bytes.Repeat([]byte("A"), 512<<10)
	for i := 0; i < 40; i++ {
		name := filepath.ToSlash(filepath.Join(".hidden", "blob-"+string(rune('a'+i/26))+string(rune('a'+i%26))))
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(chunk)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(chunk)
	}
	_ = tw.Close()
	_ = gz.Close()

	if _, err := mgr.InstallArchive(bytes.NewReader(buf.Bytes()), 4<<20, ScopeGlobal); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("expected ErrInvalidArchive for hidden tar bomb, got %v", err)
	}
}

func TestRunActionForCallerExposureAndAtomicCheck(t *testing.T) {
	globalDir := t.TempDir()
	abDir := filepath.Join(globalDir, "abilities", "gate-tool")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"ability.json": `{
			"schema":"interseptor.ability/v1",
			"id":"gate-tool",
			"name":"Gate Tool",
			"version":"1.0.0",
			"actions":[
				{"id":"pub","name":"Public","entry":"hooks/pub.star","mcp":true},
				{"id":"priv","name":"Private","entry":"hooks/priv.star","mcp":false}
			]
		}`,
		"hooks/pub.star":  "def run(ctx):\n    return {'pub': True}\n",
		"hooks/priv.star": "def run(ctx):\n    return {'priv': True}\n",
	})
	mgr, err := NewManager(Config{GlobalDir: globalDir})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if _, err := mgr.TrustAndEnable("gate-tool", true); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RunActionForCaller(context.Background(), "gate-tool", "priv", nil, false); !errors.Is(err, ErrActionNotExposed) {
		t.Fatalf("non-UI caller on mcp:false = %v, want ErrActionNotExposed", err)
	}
	if res, err := mgr.RunActionForCaller(context.Background(), "gate-tool", "pub", nil, false); err != nil || !res["ok"].(bool) {
		t.Fatalf("non-UI caller on mcp:true failed: res=%+v err=%v", res, err)
	}
	if res, err := mgr.RunActionForCaller(context.Background(), "gate-tool", "priv", nil, true); err != nil || !res["ok"].(bool) {
		t.Fatalf("UI caller on mcp:false failed: res=%+v err=%v", res, err)
	}
}

func TestStarlarkConvertBudgetPreventsDAGBlowup(t *testing.T) {
	globalDir := t.TempDir()
	abDir := filepath.Join(globalDir, "abilities", "dag-boom")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"ability.json": `{"schema":"interseptor.ability/v1","id":"dag-boom","name":"DAG","version":"1.0.0","actions":[{"id":"boom","name":"Boom","entry":"hooks/boom.star"}]}`,
		"hooks/boom.star": `def run(ctx):
    a = ["x" * 256]
    for _ in range(22):
        a = [a, a]
    return a
`,
	})
	mgr, err := NewManager(Config{GlobalDir: globalDir})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if _, err := mgr.TrustAndEnable("dag-boom", true); err != nil {
		t.Fatal(err)
	}
	_, err = mgr.RunAction(context.Background(), "dag-boom", "boom", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected conversion budget error for DAG blowup, got %v", err)
	}
}

type blockingHookStore struct {
	mu      sync.Mutex
	started chan struct{}
	once    sync.Once
	release chan struct{}
	tags    int
}

func (b *blockingHookStore) GetFlow(id int64) (*store.Flow, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return &store.Flow{ID: id, Host: "example.com", Method: "GET", Path: "/"}, nil
}
func (b *blockingHookStore) OpenBody(string) (io.ReadCloser, error) { return nil, os.ErrNotExist }

func (b *blockingHookStore) AddFlowTags(int64, []string) ([]string, error) {
	b.mu.Lock()
	b.tags++
	b.mu.Unlock()
	return nil, nil
}
func (b *blockingHookStore) SetFlowNote(int64, string) error { return nil }

func TestManagerCloseUnregistersHookAndDrains(t *testing.T) {
	plugin.Reset()
	defer plugin.Reset()

	globalDir := t.TempDir()
	abDir := createSampleAbility(t, globalDir, "close-hook", "1.0.0")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/on_flow.star": "def on_flow(flow):\n    return {'tags': ['ran']}\n",
	})
	st := &blockingHookStore{started: make(chan struct{}), release: make(chan struct{})}
	mgr, err := NewManager(Config{GlobalDir: globalDir, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterPluginHook()
	if _, err := mgr.TrustAndEnable("close-hook", true); err != nil {
		t.Fatal(err)
	}

	plugin.EmitFlowCaptured(1)
	select {
	case <-st.started:
	case <-time.After(2 * time.Second):
		t.Fatal("hook worker did not start")
	}
	close(st.release)
	mgr.Close()

	// Emitting after Close must not run or panic.
	plugin.EmitFlowCaptured(2)
	mgr.WaitHooks()
}
