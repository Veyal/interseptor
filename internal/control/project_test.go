package control

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestPortableProjectExportFailsWhenMetadataCannotBeRead(t *testing.T) {
	h, st, _ := newHub(t)
	dbPath := filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE rules`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(rec, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 instead of an incomplete export", rec.Code)
	}
}

func TestPortableProjectExportFailsWhenFlowBodyIsMissing(t *testing.T) {
	h, st, _ := newHub(t)
	_, err := st.InsertFlow(&store.Flow{
		TS:          time.UnixMilli(1),
		Method:      "POST",
		Scheme:      "https",
		Host:        "example.com",
		Port:        443,
		Path:        "/upload",
		Status:      200,
		ReqBodyHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(rec, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 instead of an export with an empty request body", rec.Code)
	}
}

func TestPortableProjectExportDoesNotTruncateLargeHistory(t *testing.T) {
	_, st, _ := newHub(t)
	const flowCount = 3
	for i := 0; i < flowCount; i++ {
		if _, err := st.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https",
			Host: "example.com", Port: 443, Path: "/history", Status: 200,
		}); err != nil {
			t.Fatalf("InsertFlow %d: %v", i, err)
		}
	}

	flows, err := portableProjectFlows(st, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != flowCount {
		t.Fatalf("export query returned %d flows, want %d", len(flows), flowCount)
	}
}

// A stray projects/default directory must not duplicate the reserved "default"
// entry (the root project) that availableProjects always lists first.
func TestPortableProjectIncludesUpstreamProxyCAButImportKeepsLocal(t *testing.T) {
	h, st, _ := newHub(t)
	want := strings.TrimSpace(testCertificatePEM(t))
	if err := st.SetSetting("upstream.proxyCA", want); err != nil {
		t.Fatal(err)
	}

	api := &projectAPI{Hub: h}
	export := httptest.NewRecorder()
	api.exportProject(export, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if export.Code != http.StatusOK {
		t.Fatalf("export status %d", export.Code)
	}
	var bundle projectBundle
	if err := json.NewDecoder(export.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Settings["upstream.proxyCA"] != want {
		t.Fatalf("exported upstream CA = %q, want %q", bundle.Settings["upstream.proxyCA"], want)
	}

	// The upstream proxy and its CA are this machine's network settings: a
	// portable import reports them as kept local instead of applying them.
	h2, st2, _ := newHub(t)
	called := false
	h2.SetUpstreamProxyCA = func([]byte) error { called = true; return nil }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{"upstream.proxyCA": "\n" + want + "\n"}})
	if err != nil {
		t.Fatal(err)
	}
	imported := httptest.NewRecorder()
	(&projectAPI{Hub: h2}).importProject(imported, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if imported.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", imported.Code, imported.Body.String())
	}
	if called {
		t.Fatal("import applied the bundle's upstream CA")
	}
	if _, ok, _ := st2.GetSetting("upstream.proxyCA"); ok {
		t.Fatal("import stored the bundle's upstream CA")
	}
	if !strings.Contains(imported.Body.String(), `"keptLocalSettings":["upstream.proxyCA"]`) {
		t.Fatalf("import result does not name the kept setting: %s", imported.Body.String())
	}
}

func TestPortableProjectIncludesAndAppliesOriginTLSSettings(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting(originTLSVerifySettingKey, "1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(originTLSVerifyBypassSettingKey, "*.test.example\napi.example"); err != nil {
		t.Fatal(err)
	}

	export := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(export, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if export.Code != http.StatusOK {
		t.Fatalf("export status = %d", export.Code)
	}
	var bundle projectBundle
	if err := json.NewDecoder(export.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Settings[originTLSVerifySettingKey] != "1" || bundle.Settings[originTLSVerifyBypassSettingKey] != "*.test.example\napi.example" {
		t.Fatalf("origin TLS settings = %#v", bundle.Settings)
	}

	h2, st2, _ := newHub(t)
	var gotVerify bool
	var gotHosts []string
	h2.SetOriginTLSVerify = func(v bool) { gotVerify = v }
	h2.SetOriginTLSVerifyBypassHosts = func(hosts []string) { gotHosts = append([]string(nil), hosts...) }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{
		originTLSVerifySettingKey: "0", originTLSVerifyBypassSettingKey: " *.example.com\napi.example\n*.example.com",
	}})
	if err != nil {
		t.Fatal(err)
	}
	imported := httptest.NewRecorder()
	(&projectAPI{Hub: h2}).importProject(imported, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if imported.Code != http.StatusOK {
		t.Fatalf("import status = %d: %s", imported.Code, imported.Body.String())
	}
	if gotVerify {
		t.Fatal("origin TLS verification callback received true, want false")
	}
	if want := []string{"*.example.com", "api.example"}; !reflect.DeepEqual(gotHosts, want) {
		t.Fatalf("origin TLS exception callback = %v, want %v", gotHosts, want)
	}
	if got, ok, _ := st2.GetSetting(originTLSVerifySettingKey); !ok || got != "0" {
		t.Fatalf("stored origin TLS verification = %q, ok=%v", got, ok)
	}
	if got, ok, _ := st2.GetSetting(originTLSVerifyBypassSettingKey); !ok || got != "*.example.com\napi.example" {
		t.Fatalf("stored origin TLS exceptions = %q, ok=%v", got, ok)
	}
}

func TestPortableProjectRejectsInvalidOriginTLSVerificationBeforeApplying(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting(originTLSVerifySettingKey, "1"); err != nil {
		t.Fatal(err)
	}
	called := false
	h.SetOriginTLSVerify = func(bool) { called = true }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{
		originTLSVerifySettingKey: "2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(rr, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d, want 400", rr.Code)
	}
	if called {
		t.Fatal("origin TLS verification callback called for invalid bundle")
	}
	if got, ok, _ := st.GetSetting(originTLSVerifySettingKey); !ok || got != "1" {
		t.Fatalf("invalid bundle changed origin TLS setting to %q, ok=%v", got, ok)
	}
}

func TestAvailableProjectsSkipsReservedDefault(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"default", "beta", "acme"} {
		if err := os.MkdirAll(filepath.Join(tmp, "projects", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := (&projectAPI{&Hub{GlobalDir: tmp}}).availableProjects()
	want := []string{"default", "acme", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("availableProjects() = %v, want %v", got, want)
	}
}

// scheduleProjectSwitch must dedup: rapid repeated switches cancel earlier
// pending timers so only the LAST target fires exactly once. Without the
// cancelable timer, every request would stack a delayed re-exec.
func TestScheduleProjectSwitchDedups(t *testing.T) {
	var mu sync.Mutex
	var fired []string
	h := &Hub{}
	h.SwitchProject = func(target string) error {
		mu.Lock()
		fired = append(fired, target)
		mu.Unlock()
		return nil
	}

	// Fire several switches in quick succession; each within the delay window.
	for _, target := range []string{"a", "b", "c"} {
		h.scheduleProjectSwitch(target, 40*time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Fatalf("expected exactly 1 switch to fire, got %d: %v", len(fired), fired)
	}
	if fired[0] != "c" {
		t.Fatalf("expected only the latest target 'c' to fire, got %q", fired[0])
	}
}

func TestExternalProjectSwitchDedups(t *testing.T) {
	var mu sync.Mutex
	var fired []string
	h := &Hub{GlobalDir: t.TempDir()}
	h.SwitchProject = func(target string) error {
		mu.Lock()
		fired = append(fired, target)
		mu.Unlock()
		return nil
	}
	api := &projectAPI{h}
	for _, path := range []string{filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")} {
		body, err := json.Marshal(map[string]string{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		api.switchProject(rec, httptest.NewRequest(http.MethodPost, "/api/project/switch", bytes.NewReader(body)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("switch %q status = %d; body = %s", path, rec.Code, rec.Body.String())
		}
	}
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || filepath.Base(fired[0]) != "second" {
		t.Fatalf("external switches fired %v, want only the latest path", fired)
	}
}

func TestHubCloseCancelsPendingProjectSwitch(t *testing.T) {
	fired := make(chan struct{}, 1)
	h := &Hub{SwitchProject: func(string) error { fired <- struct{}{}; return nil }}
	h.scheduleProjectSwitch("later", 100*time.Millisecond)
	h.Close()
	select {
	case <-fired:
		t.Fatal("project switch fired after Hub.Close")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestExternalProjectSwitchRejectsRegistryPersistenceFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	fired := make(chan string, 1)
	h := &Hub{GlobalDir: blocked, SwitchProject: func(target string) error { fired <- target; return nil }}
	defer h.Close()
	external := filepath.Join(t.TempDir(), "engagement")
	body, err := json.Marshal(map[string]string{"path": external})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).switchProject(rec, httptest.NewRequest(http.MethodPost, "/api/project/switch", bytes.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	select {
	case target := <-fired:
		t.Fatalf("project switch fired despite registry failure: %q", target)
	case <-time.After(350 * time.Millisecond):
	}
}

func TestExternalProjectSwitchRejectsUnusableFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	global := t.TempDir()
	fired := make(chan string, 1)
	h := &Hub{GlobalDir: global, SwitchProject: func(target string) error { fired <- target; return nil }}
	defer h.Close()
	body, _ := json.Marshal(map[string]string{"path": filepath.Join(file, "engagement")})
	rec := httptest.NewRecorder()
	(&projectAPI{h}).switchProject(rec, httptest.NewRequest(http.MethodPost, "/api/project/switch", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if list := readExternalProjects(global); len(list) != 0 {
		t.Fatalf("unusable folder was remembered: %+v", list)
	}
	select {
	case target := <-fired:
		t.Fatalf("switch fired for an unusable folder: %q", target)
	case <-time.After(350 * time.Millisecond):
	}
}

// QA round 2: the portable project JSON (Settings → Project → Export project,
// the shareable bundle) carried the upstream proxy password in plaintext.
// The bundle keeps the proxy URL and user but never a saved password, and
// re-importing it where the same proxy is configured keeps the local password.
func TestPortableProjectExportOmitsSavedProxyPasswords(t *testing.T) {
	h, st, _ := newHub(t)
	for k, v := range map[string]string{
		"upstream.proxy":     "http://bob:s3cretPW@proxy.example.com:3128",
		proxyAuthEnabledKey:  "1",
		proxyAuthUserKey:     "qa",
		proxyAuthPasswordKey: "listenerPW",
	} {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	export := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(export, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if export.Code != http.StatusOK {
		t.Fatalf("export status %d", export.Code)
	}
	raw := export.Body.Bytes()
	for _, secret := range []string{"s3cretPW", "listenerPW"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("portable export leaks %q: %s", secret, raw)
		}
	}
	var bundle projectBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if got := bundle.Settings["upstream.proxy"]; got != "http://bob@proxy.example.com:3128" {
		t.Fatalf("exported upstream = %q, want the URL without its password", got)
	}

	h.Upstream = func(string) error { return nil }
	imported := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(imported, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(raw)))
	if imported.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", imported.Code, imported.Body.String())
	}
	if got, _, _ := st.GetSetting("upstream.proxy"); got != "http://bob:s3cretPW@proxy.example.com:3128" {
		t.Fatalf("re-import over the same proxy dropped the local password: %q", got)
	}
}
