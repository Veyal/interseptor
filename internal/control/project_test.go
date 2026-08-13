package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// A stray projects/default directory must not duplicate the reserved "default"
// entry (the root project) that availableProjects always lists first.
func TestPortableProjectIncludesAndAppliesUpstreamProxyCA(t *testing.T) {
	h, st, _ := newHub(t)
	want := "-----BEGIN CERTIFICATE-----\ngeneric-ca\n-----END CERTIFICATE-----"
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

	h2, st2, _ := newHub(t)
	var got string
	h2.SetUpstreamProxyCA = func(v []byte) error { got = string(v); return nil }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{"upstream.proxyCA": "\n" + want + "\n"}})
	if err != nil {
		t.Fatal(err)
	}
	imported := httptest.NewRecorder()
	(&projectAPI{Hub: h2}).importProject(imported, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if imported.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", imported.Code, imported.Body.String())
	}
	if got != want {
		t.Fatalf("SetUpstreamProxyCA = %q, want %q", got, want)
	}
	stored, ok, err := st2.GetSetting("upstream.proxyCA")
	if err != nil || !ok || stored != want {
		t.Fatalf("stored upstream CA = %q, %v, %v; want %q, true, nil", stored, ok, err, want)
	}
}

func TestPortableProjectRejectsInvalidUpstreamCAWithoutApplyingProxy(t *testing.T) {
	h, st, _ := newHub(t)
	proxyCalled := false
	h.SetUpstreamProxyCA = func([]byte) error { return errors.New("invalid certificate") }
	h.Upstream = func(string) error { proxyCalled = true; return nil }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{
		"upstream.proxyCA": "invalid", "upstream.proxy": "http://proxy.example:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(rr, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("import status %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if proxyCalled {
		t.Fatal("upstream proxy applied after invalid CA")
	}
	if _, ok, _ := st.GetSetting("upstream.proxy"); ok {
		t.Fatal("invalid import persisted upstream proxy")
	}
	if _, ok, _ := st.GetSetting("upstream.proxyCA"); ok {
		t.Fatal("invalid import persisted upstream CA")
	}
}

func TestPortableProjectRejectsUpstreamProxyFailureWithoutPersistingCA(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting("upstream.proxyCA", "previous"); err != nil {
		t.Fatal(err)
	}
	var applied []string
	h.SetUpstreamProxyCA = func(v []byte) error { applied = append(applied, string(v)); return nil }
	h.Upstream = func(string) error { return errors.New("invalid proxy") }
	payload, err := json.Marshal(projectBundle{Version: "1", Settings: map[string]string{
		"upstream.proxyCA": "new", "upstream.proxy": "http://proxy.example:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(rr, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(payload)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("import status %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if len(applied) != 2 || applied[0] != "new" || applied[1] != "previous" {
		t.Fatalf("runtime CA applications = %q, want [new previous]", applied)
	}
	stored, ok, err := st.GetSetting("upstream.proxyCA")
	if err != nil || !ok || stored != "previous" {
		t.Fatalf("stored upstream CA = %q, %v, %v; want previous, true, nil", stored, ok, err)
	}
	if _, ok, _ := st.GetSetting("upstream.proxy"); ok {
		t.Fatal("failed import persisted upstream proxy")
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
