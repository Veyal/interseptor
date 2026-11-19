package abilities

import (
	"context"
	"encoding/json"
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

// Compile-time check that *store.Store satisfies FlowStore.
var _ FlowStore = (*store.Store)(nil)

type fakeFlowStore struct {
	mu      sync.Mutex
	flows   map[int64]*store.Flow
	bodies  map[string]string
	tags    map[int64][]string
	notes   map[int64]string
	getGate chan struct{}
	doneCh  chan int64
}

func newFakeFlowStore() *fakeFlowStore {
	return &fakeFlowStore{
		flows:  map[int64]*store.Flow{},
		bodies: map[string]string{},
		tags:   map[int64][]string{},
		notes:  map[int64]string{},
	}
}

func (f *fakeFlowStore) GetFlow(id int64) (*store.Flow, error) {
	if f.getGate != nil {
		<-f.getGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[id]
	if !ok {
		return nil, io.EOF
	}
	cp := *fl
	return &cp, nil
}

func (f *fakeFlowStore) OpenBody(sum string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return io.NopCloser(strings.NewReader(f.bodies[sum])), nil
}

func (f *fakeFlowStore) AddFlowTags(flowID int64, tags []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[flowID] = append(f.tags[flowID], tags...)
	if f.doneCh != nil {
		select {
		case f.doneCh <- flowID:
		default:
		}
	}
	return append([]string(nil), f.tags[flowID]...), nil
}

func (f *fakeFlowStore) SetFlowNote(id int64, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes[id] = note
	if f.doneCh != nil {
		select {
		case f.doneCh <- id:
		default:
		}
	}
	return nil
}

func TestStarlarkFlowHookExecutionAndNonBlocking(t *testing.T) {
	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "flow-tagger", "1.0.0")
	// Overwrite on_flow.star to inspect flow dict, starx helpers, and return tags+note
	hookSrc := `
def on_flow(flow):
    if flow["host"] == "example.com" and len(flow["reqBody"]) > 0:
        parsed = json_decode(flow["reqBody"])
        digest = sha256_hex(parsed["token"])
        return {
            "tags": ["has-token", "  ", "sha-" + digest[:8]],
            "note": "method=" + flow["method"] + " body_len=" + str(len(flow["resBody"]))
        }
    return None
`
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/on_flow.star": hookSrc,
	})

	fs := newFakeFlowStore()
	fs.getGate = make(chan struct{})
	fs.bodies["req1"] = `{"token":"abc123"}`
	fs.bodies["res1"] = strings.Repeat("Z", MaxHookBodyBytes+1024) // exceeds 64 KiB cap
	fs.flows[10] = &store.Flow{
		ID:          10,
		Method:      "POST",
		Scheme:      "https",
		Host:        "example.com",
		Port:        443,
		Path:        "/api/login",
		Status:      200,
		Mime:        "application/json",
		ReqHeaders:  map[string][]string{"Authorization": {"Bearer x"}},
		ResHeaders:  map[string][]string{"Content-Type": {"application/json"}},
		ReqBodyHash: "req1",
		ResBodyHash: "res1",
		Note:        "",
	}

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: fs})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Quarantined -> must NOT execute hook
	close(fs.getGate)
	mgr.HandleFlowCaptured(10)
	mgr.WaitHooks()
	if len(fs.tags[10]) != 0 || fs.notes[10] != "" {
		t.Fatalf("quarantined ability executed hook! tags=%v note=%q", fs.tags[10], fs.notes[10])
	}

	// Trust & enable, then test non-blocking while getGate is blocked!
	if _, err := mgr.TrustAndEnable("flow-tagger", true); err != nil {
		t.Fatal(err)
	}
	fs.getGate = make(chan struct{})
	start := time.Now()
	mgr.HandleFlowCaptured(10)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("HandleFlowCaptured blocked for %v", elapsed)
	}
	close(fs.getGate)
	mgr.WaitHooks()

	fs.mu.Lock()
	gotTags := append([]string(nil), fs.tags[10]...)
	gotNote := fs.notes[10]
	fs.mu.Unlock()

	if len(gotTags) != 2 || gotTags[0] != "has-token" || !strings.HasPrefix(gotTags[1], "sha-") {
		t.Fatalf("gotTags = %v", gotTags)
	}
	wantNote := "method=POST body_len=65536"
	if gotNote != wantNote {
		t.Fatalf("gotNote = %q want %q", gotNote, wantNote)
	}
}

func TestStarlarkHookStepLimitAndPluginRegistration(t *testing.T) {
	plugin.Reset()
	defer plugin.Reset()

	projectDir := t.TempDir()

	// Ability 1: infinite-style nested loop exceeding 100k steps
	abLoop := createSampleAbility(t, projectDir, "loop-ability", "1.0.0")
	writeTestAbilityFiles(t, abLoop, map[string]string{
		"hooks/on_flow.star": "def on_flow(flow):\n    x = 0\n    for i in range(1000):\n        for j in range(1000):\n            x += 1\n    return {'tags': ['never']}\n",
	})

	// Ability 2: valid hook that runs after Ability 1 fails
	abGood := createSampleAbility(t, projectDir, "good-ability", "1.0.0")
	writeTestAbilityFiles(t, abGood, map[string]string{
		"hooks/on_flow.star": "def on_flow(flow):\n    return {'tags': ['from-plugin']}\n",
	})

	fs := newFakeFlowStore()
	fs.flows[1] = &store.Flow{ID: 1, Host: "example.com", Method: "GET", Path: "/"}

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: fs})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if _, err := mgr.TrustAndEnable("loop-ability", true); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.TrustAndEnable("good-ability", true); err != nil {
		t.Fatal(err)
	}
	mgr.RegisterPluginHook()
	mgr.RegisterPluginHook() // idempotent

	plugin.EmitFlowCaptured(1)
	mgr.WaitHooks()

	fs.mu.Lock()
	got := append([]string(nil), fs.tags[1]...)
	fs.mu.Unlock()
	if len(got) != 1 || got[0] != "from-plugin" {
		t.Fatalf("expected only ['from-plugin'], got %v", got)
	}
}

func TestStarlarkActionExecutionAndBounds(t *testing.T) {
	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "action-tester", "1.0.0")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/action.star": `
def run(ctx):
    f = ctx.get("flow")
    decoded = starx.b64encode(ctx["msg"])
    return {
        "echo": ctx["msg"],
        "b64": decoded,
        "host": f["host"] if f else "",
        "headerCount": len(f["reqHeaders"]) if f else 0,
    }
`,
	})

	fs := newFakeFlowStore()
	fs.flows[77] = &store.Flow{
		ID:         77,
		Method:     "GET",
		Scheme:     "https",
		Host:       "api.example.com",
		Port:       443,
		Path:       "/v1/items",
		Status:     200,
		ReqHeaders: map[string][]string{"Accept": {"application/json"}},
	}

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: fs})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Must refuse when quarantined
	if _, err := mgr.RunAction(context.Background(), "action-tester", "ping", map[string]any{"msg": "hi"}); err == nil {
		t.Fatal("expected error running action on quarantined ability")
	}

	if _, err := mgr.TrustAndEnable("action-tester", true); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.RunAction(context.Background(), "action-tester", "ping", map[string]any{
		"msg":    "hello",
		"flowId": float64(77), // test JSON float64 number decoding
	})
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}

	rawJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("result not JSON serializable: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rawJSON, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["ok"] != true {
		t.Fatalf("expected ok=true, got %+v", parsed)
	}
	inner, ok := parsed["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", parsed["result"])
	}
	if inner["host"] != "api.example.com" || inner["b64"] != "aGVsbG8=" {
		t.Fatalf("unexpected inner result: %+v", inner)
	}

	// Context cancellation stops action
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mgr.RunAction(canceled, "action-tester", "ping", map[string]any{"msg": "hi"}); err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestRealStoreIntegration(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Verify FlowStore interface works with *store.Store end-to-end.
	flowID, err := st.InsertFlow(&store.Flow{
		TS:         time.UnixMilli(1_700_000_000_000),
		Method:     "GET",
		Scheme:     "https",
		Host:       "example.com",
		Port:       443,
		Path:       "/api/profile",
		Status:     200,
		ReqHeaders: map[string][]string{"Accept": {"application/json"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
		Mime:       "application/json",
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}

	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "real-store-hook", "1.0.0")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/on_flow.star": "def on_flow(flow):\n    if flow['host'] == 'example.com':\n        return {'tags': ['example-hit'], 'note': 'seen ' + flow['path']}\n    return None\n",
		"hooks/action.star":  "def run(ctx):\n    return {'path': ctx['flow']['path'], 'status': ctx['flow']['status']}\n",
	})

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if _, err := mgr.TrustAndEnable("real-store-hook", true); err != nil {
		t.Fatal(err)
	}

	mgr.HandleFlowCaptured(flowID)
	mgr.WaitHooks()

	gotFlow, err := st.GetFlow(flowID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if gotFlow.Note != "seen /api/profile" {
		t.Fatalf("flow note = %q want %q", gotFlow.Note, "seen /api/profile")
	}
	tags, err := st.FlowTags(flowID)
	if err != nil {
		t.Fatalf("FlowTags: %v", err)
	}
	if len(tags) != 1 || tags[0] != "example-hit" {
		t.Fatalf("flow tags = %v want [example-hit]", tags)
	}

	res, err := mgr.RunAction(context.Background(), "real-store-hook", "ping", map[string]any{"flowId": flowID})
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	inner := res["result"].(map[string]any)
	if inner["path"] != "/api/profile" || inner["status"] != int64(200) {
		t.Fatalf("unexpected action result: %+v", inner)
	}
}

func TestStarlarkSandboxRestrictionsAndBounds(t *testing.T) {
	projectDir := t.TempDir()
	abDir := createSampleAbility(t, projectDir, "bounds-test", "1.0.0")
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/on_flow.star": `
def on_flow(flow):
    # Return > MaxHookTags tags and an overly long tag + whitespace note (should be ignored)
    tags = ["tag-" + str(i) for i in range(30)]
    tags.append("x" * 120)
    return {"tags": tags, "note": "   "}
`,
		"hooks/action.star": `
def run(ctx):
    if ctx.get("mode") == "cycle":
        x = []
        x.append(x)
        return x
    if ctx.get("mode") == "mutate":
        ctx["flow"]["host"] = "evil.example.com"
        return {}
    if ctx.get("mode") == "steps":
        s = 0
        for i in range(1000):
            for j in range(1000):
                s += 1
        return {"s": s}
    return {"ok": True}
`,
	})

	fs := newFakeFlowStore()
	fs.flows[5] = &store.Flow{ID: 5, Host: "example.com", Note: "keep-me"}
	fs.notes[5] = "keep-me"

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: fs})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if _, err := mgr.TrustAndEnable("bounds-test", true); err != nil {
		t.Fatal(err)
	}

	mgr.HandleFlowCaptured(5)
	mgr.WaitHooks()
	if len(fs.tags[5]) != MaxHookTags {
		t.Fatalf("tags len = %d want %d", len(fs.tags[5]), MaxHookTags)
	}
	if fs.notes[5] != "keep-me" {
		t.Fatalf("blank hook note overwrote existing note: %q", fs.notes[5])
	}

	for _, mode := range []string{"cycle", "mutate", "steps"} {
		if _, err := mgr.RunAction(context.Background(), "bounds-test", "ping", map[string]any{"flowId": 5, "mode": mode}); err == nil {
			t.Fatalf("expected error for mode %q", mode)
		}
	}

	// Verify load() is disabled in Starlark scripts
	writeTestAbilityFiles(t, abDir, map[string]string{
		"hooks/action.star": "load('other.star', 'x')\ndef run(ctx):\n    return {}\n",
	})
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	ab, _ := mgr.Get("bounds-test")
	if ab.LoadError == "" {
		t.Fatal("expected LoadError when script uses load()")
	}
}

func TestExamplesAbilitiesCompatibility(t *testing.T) {
	exRoot := filepath.Join("..", "..", "examples", "abilities")
	if _, err := os.Stat(exRoot); err != nil {
		t.Skip("examples/abilities not present")
	}

	projectDir := t.TempDir()
	for _, name := range []string{"hello-ability", "jwt-xray"} {
		src := filepath.Join(exRoot, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(projectDir, "abilities", name)
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0o644)
		})
		if err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}

	fs := newFakeFlowStore()
	// Unsigned alg=none JWT: {"alg":"none","typ":"JWT"}.{"sub":"user1","password":"leak"}.
	algNoneJWT := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ1c2VyMSIsInBhc3N3b3JkIjoibGVhayJ9."
	fs.flows[101] = &store.Flow{
		ID:         101,
		Method:     "GET",
		Scheme:     "https",
		Host:       "api.example.com",
		Port:       443,
		Path:       "/api/me",
		Status:     200,
		ReqHeaders: map[string][]string{"Authorization": {"Bearer " + algNoneJWT}},
	}

	mgr, err := NewManager(Config{GlobalDir: t.TempDir(), ProjectDir: projectDir, Store: fs})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	for _, ab := range mgr.List() {
		if ab.LoadError != "" {
			t.Fatalf("example ability %s LoadError = %s", ab.ID, ab.LoadError)
		}
		if _, err := mgr.TrustAndEnable(ab.ID, true); err != nil {
			t.Fatalf("TrustAndEnable(%s): %v", ab.ID, err)
		}
	}

	mgr.HandleFlowCaptured(101)
	mgr.WaitHooks()

	fs.mu.Lock()
	tags := append([]string(nil), fs.tags[101]...)
	fs.mu.Unlock()
	if len(tags) < 3 {
		t.Fatalf("expected tags from hello-ability and jwt-xray, got %v", tags)
	}

	if _, ok := mgr.Get("jwt-xray"); ok {
		res, err := mgr.RunAction(context.Background(), "jwt-xray", "analyze_flow", map[string]any{"flowId": 101})
		if err != nil {
			t.Fatalf("RunAction jwt-xray: %v", err)
		}
		inner := res["result"].(map[string]any)
		if inner["count"] != int64(1) {
			t.Fatalf("jwt-xray count = %v want 1 (%+v)", inner["count"], inner)
		}
	}
	if _, ok := mgr.Get("hello-ability"); ok {
		res, err := mgr.RunAction(context.Background(), "hello-ability", "summarize_flow", map[string]any{"flowId": 101})
		if err != nil {
			t.Fatalf("RunAction hello-ability: %v", err)
		}
		inner := res["result"].(map[string]any)
		if inner["host"] != "api.example.com" {
			t.Fatalf("hello-ability host = %v want api.example.com", inner["host"])
		}
	}
}
