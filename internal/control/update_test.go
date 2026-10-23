package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/version"
)

// fakeUpdater scripts the self-update surface. Stage blocks on gate (when set)
// so a test can observe the running state.
type fakeUpdater struct {
	mu       sync.Mutex
	install  version.InstallInfo
	latest   string
	notes    version.ReleaseNotes
	stageErr error
	gate     chan struct{}
	staged   int
	progress []version.StageProgress
}

func (f *fakeUpdater) Install() version.InstallInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.install
}

func (f *fakeUpdater) set(fn func(*fakeUpdater)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeUpdater) stagedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.staged
}
func (f *fakeUpdater) Latest(context.Context) (string, error) {
	return f.latest, nil
}
func (f *fakeUpdater) Notes(_ context.Context, ver string) (version.ReleaseNotes, error) {
	n := f.notes
	n.Version = ver
	return n, nil
}
func (f *fakeUpdater) Stage(ctx context.Context, ver string, progress func(version.StageProgress)) (version.StagedUpdate, error) {
	f.mu.Lock()
	f.staged++
	steps := f.progress
	gate := f.gate
	stageErr := f.stageErr
	f.mu.Unlock()
	for _, p := range steps {
		progress(p)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return version.StagedUpdate{}, ctx.Err()
		}
	}
	if stageErr != nil {
		return version.StagedUpdate{}, stageErr
	}
	return version.StagedUpdate{Version: ver, Dest: "/tmp/fake/interseptor"}, nil
}

func newUpdateHub(t *testing.T, f *fakeUpdater) (*Hub, *httptest.Server, chan string) {
	t.Helper()
	h, _, _ := newHub(t)
	h.Updater = f
	h.SetUpdate(f.latest, version.IsNewer(version.String(), f.latest))
	ch := make(chan string, 256)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts, ch
}

func updateReq(t *testing.T, ts *httptest.Server, method, path string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, nil)
	req.Header.Set("X-Interseptor-CSRF", "1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func waitPhase(t *testing.T, ts *httptest.Server, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, out := updateReq(t, ts, http.MethodGet, "/api/update", nil)
		if st, _ := out["state"].(map[string]any); st != nil && st["phase"] == want {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase %q never reached", want)
	return nil
}

func drainEvents(ch chan string) []map[string]any {
	var out []map[string]any
	for {
		select {
		case s := <-ch:
			var m map[string]any
			if json.Unmarshal([]byte(s), &m) == nil && m["type"] == "update.progress" {
				out = append(out, m)
			}
		default:
			return out
		}
	}
}

const newerRelease = "999.0.0"

func TestUpdateGetReportsReleaseAndMethod(t *testing.T) {
	f := &fakeUpdater{
		install: version.InstallInfo{Method: version.InstallBrew, Command: "brew upgrade interseptor", Reason: "managed"},
		latest:  newerRelease,
		notes:   version.ReleaseNotes{Body: "## Notes", URL: "https://github.com/x/releases/tag/v999.0.0"},
	}
	_, ts, _ := newUpdateHub(t, f)
	code, out := updateReq(t, ts, http.MethodGet, "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	if out["current"] != version.String() || out["latest"] != newerRelease || out["available"] != true ||
		out["notes"] != "## Notes" || out["method"] != "brew" || out["command"] != "brew upgrade interseptor" {
		t.Fatalf("GET /api/update = %v", out)
	}
	if st, _ := out["state"].(map[string]any); st == nil || st["phase"] != "idle" {
		t.Fatalf("state = %v", out["state"])
	}
}

func TestUpdatePostReachesReadyWithProgress(t *testing.T) {
	f := &fakeUpdater{
		install: version.InstallInfo{Method: version.InstallSelf},
		latest:  newerRelease,
		progress: []version.StageProgress{
			{Phase: version.PhaseDownloading, Done: 0, Total: 100},
			{Phase: version.PhaseDownloading, Done: 100, Total: 100},
			{Phase: version.PhaseVerifying, Done: 100, Total: 100},
			{Phase: version.PhaseInstalling, Done: 100, Total: 100},
		},
	}
	_, ts, ch := newUpdateHub(t, f)
	if code, out := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusAccepted {
		t.Fatalf("POST = %d %v", code, out)
	}
	waitPhase(t, ts, "ready")
	events := drainEvents(ch)
	var phases []string
	for _, e := range events {
		if p, _ := e["phase"].(string); len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	}
	if strings.Join(phases, ",") != "downloading,verifying,installing,ready" {
		t.Fatalf("SSE phases = %v (events %v)", phases, events)
	}
	last := events[len(events)-1]
	if last["done"] != float64(100) || last["total"] != float64(100) {
		t.Fatalf("final event = %v", last)
	}
	// A staged update is not restaged.
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusConflict {
		t.Fatalf("POST after ready = %d, want 409", code)
	}
}

func TestUpdatePostSingleFlight(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: newerRelease, gate: make(chan struct{})}
	_, ts, _ := newUpdateHub(t, f)
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusAccepted {
		t.Fatalf("first POST = %d", code)
	}
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusConflict {
		t.Fatalf("second POST while running = %d, want 409", code)
	}
	close(f.gate)
	waitPhase(t, ts, "ready")
	if n := f.stagedCount(); n != 1 {
		t.Fatalf("Stage called %d times", n)
	}
}

func TestUpdatePostRefusesAgentsAndManagedInstalls(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: newerRelease}
	_, ts, _ := newUpdateHub(t, f)
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", map[string]string{"X-Interseptor-Source": "ai"}); code != http.StatusForbidden {
		t.Fatalf("AI-sourced POST = %d, want 403", code)
	}
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update/restart", map[string]string{"X-Interseptor-Source": "ai"}); code != http.StatusForbidden {
		t.Fatalf("AI-sourced restart = %d, want 403", code)
	}
	for _, info := range []version.InstallInfo{
		{Method: version.InstallBrew, Command: "brew upgrade interseptor", Reason: "managed by Homebrew"},
		{Method: version.InstallScoop, Command: "scoop update interseptor", Reason: "managed by Scoop"},
		{Method: version.InstallDesktop, Reason: "the macOS app updates itself"},
		{Method: version.InstallDev, Reason: "local build"},
		{Method: version.InstallReadonly, Reason: "not writable"},
	} {
		f.set(func(f *fakeUpdater) { f.install = info })
		code, out := updateReq(t, ts, http.MethodPost, "/api/update", nil)
		msg, _ := out["error"].(string)
		if code != http.StatusConflict || !strings.Contains(msg, info.Reason) || (info.Command != "" && !strings.Contains(msg, info.Command)) {
			t.Fatalf("%s: POST = %d %q", info.Method, code, msg)
		}
	}
	if f.stagedCount() != 0 {
		t.Fatal("a refused update must not stage anything")
	}
}

func TestUpdatePostUpToDate(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: "0.0.1"}
	_, ts, _ := newUpdateHub(t, f)
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusConflict {
		t.Fatalf("POST with no newer release = %d, want 409", code)
	}
}

func TestUpdateFailureReportsErrorAndAllowsRetry(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: newerRelease, stageErr: errors.New("checksum mismatch: got abc")}
	h, ts, ch := newUpdateHub(t, f)
	var restarted atomic.Bool
	h.RestartForUpdate = func(version.StagedUpdate) error { restarted.Store(true); return nil }
	updateReq(t, ts, http.MethodPost, "/api/update", nil)
	out := waitPhase(t, ts, "error")
	st := out["state"].(map[string]any)
	if !strings.Contains(st["error"].(string), "checksum mismatch") {
		t.Fatalf("state = %v", st)
	}
	events := drainEvents(ch)
	if last := events[len(events)-1]; last["phase"] != "error" || !strings.Contains(last["error"].(string), "checksum") {
		t.Fatalf("last event = %v", last)
	}
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update/restart", nil); code != http.StatusConflict || restarted.Load() {
		t.Fatalf("restart after failure = %d, want 409", code)
	}
	f.set(func(f *fakeUpdater) { f.stageErr = nil })
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusAccepted {
		t.Fatalf("retry POST = %d", code)
	}
	waitPhase(t, ts, "ready")
}

func TestUpdateRestartOnlyWhenReady(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: newerRelease}
	h, ts, _ := newUpdateHub(t, f)
	got := make(chan version.StagedUpdate, 1)
	h.RestartForUpdate = func(s version.StagedUpdate) error { got <- s; return nil }
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update/restart", nil); code != http.StatusConflict {
		t.Fatalf("restart before staging = %d, want 409", code)
	}
	updateReq(t, ts, http.MethodPost, "/api/update", nil)
	waitPhase(t, ts, "ready")
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update/restart", nil); code != http.StatusAccepted {
		t.Fatalf("restart when ready = %d, want 202", code)
	}
	select {
	case s := <-got:
		if s.Version != newerRelease || s.Dest == "" {
			t.Fatalf("restart hook got %+v", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restart hook never invoked")
	}
}

func TestUpdateRestartUnavailableWithoutHook(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallSelf}, latest: newerRelease}
	_, ts, _ := newUpdateHub(t, f)
	updateReq(t, ts, http.MethodPost, "/api/update", nil)
	waitPhase(t, ts, "ready")
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update/restart", nil); code != http.StatusNotImplemented {
		t.Fatalf("restart without a hook = %d, want 501", code)
	}
}

func TestUpdateWithoutUpdaterIsUnavailable(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, out := updateReq(t, ts, http.MethodGet, "/api/update", nil)
	if code != http.StatusOK || out["method"] != "unavailable" {
		t.Fatalf("GET without updater = %d %v", code, out)
	}
	if code, _ := updateReq(t, ts, http.MethodPost, "/api/update", nil); code != http.StatusNotImplemented {
		t.Fatalf("POST without updater = %d, want 501", code)
	}
}

func TestUpdateProgressThrottle(t *testing.T) {
	var th progressThrottle
	now := time.Unix(0, 0)
	if !th.allow(version.PhaseDownloading, now) {
		t.Fatal("first event must pass")
	}
	if th.allow(version.PhaseDownloading, now.Add(50*time.Millisecond)) {
		t.Fatal("a second byte update within 100ms must be dropped")
	}
	if !th.allow(version.PhaseVerifying, now.Add(60*time.Millisecond)) {
		t.Fatal("a phase change always passes")
	}
	if !th.allow(version.PhaseVerifying, now.Add(200*time.Millisecond)) {
		t.Fatal("an update after the interval passes")
	}
}
