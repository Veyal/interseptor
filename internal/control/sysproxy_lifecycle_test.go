package control

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSysProxy stubs the OS backend; the real system proxy is never touched.
type fakeSysProxy struct {
	on       bool
	disables int
	failOff  bool
}

func installFakeSysProxy(t *testing.T, h *Hub) *fakeSysProxy {
	t.Helper()
	f := &fakeSysProxy{}
	h.GlobalDir = t.TempDir()
	h.sysProxyEnable = func(string, int) error { f.on = true; return nil }
	h.sysProxyDisable = func() error {
		f.disables++
		if f.failOff {
			return errors.New("networksetup failed")
		}
		f.on = false
		return nil
	}
	h.sysProxyStatus = func() (bool, error) { return f.on, nil }
	h.sysProxySupported = func() bool { return true }
	return f
}

func postSysProxy(t *testing.T, h *Hub, enabled bool) {
	t.Helper()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body := `{"enabled":false}`
	if enabled {
		body = `{"enabled":true}`
	}
	resp, err := http.Post(ts.URL+"/api/sysproxy", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST sysproxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST sysproxy status = %d", resp.StatusCode)
	}
}

func markerExists(h *Hub) bool {
	_, err := os.Stat(filepath.Join(h.GlobalDir, sysProxyMarkerName))
	return err == nil
}

func TestSystemProxyEnablePersistsMarkerAndShutdownDisables(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	postSysProxy(t, h, true)
	if !markerExists(h) {
		t.Fatal("enabling the system proxy must persist an enabled-by-us marker")
	}
	if err := h.ReleaseSystemProxy(); err != nil {
		t.Fatalf("ReleaseSystemProxy: %v", err)
	}
	if f.on || f.disables != 1 {
		t.Fatalf("shutdown did not disable system proxy: on=%v disables=%d", f.on, f.disables)
	}
	if markerExists(h) {
		t.Fatal("marker must be cleared after a successful disable")
	}
}

func TestSystemProxyUserDisableClearsMarker(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	postSysProxy(t, h, true)
	postSysProxy(t, h, false)
	if markerExists(h) {
		t.Fatal("user disable must clear the marker")
	}
	if err := h.ReleaseSystemProxy(); err != nil {
		t.Fatal(err)
	}
	if f.disables != 1 {
		t.Fatalf("shutdown must not touch a proxy we no longer own: disables=%d", f.disables)
	}
}

func TestSystemProxyNotOwnedIsLeftAloneOnShutdownAndStartup(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	f.on = true // enabled by the user/another tool, not by us
	if err := h.ReleaseSystemProxy(); err != nil {
		t.Fatal(err)
	}
	if err := h.RecoverSystemProxy(false); err != nil {
		t.Fatal(err)
	}
	if f.disables != 0 || !f.on {
		t.Fatalf("foreign system proxy was modified: on=%v disables=%d", f.on, f.disables)
	}
}

func TestSystemProxyStaleMarkerDisabledAtStartup(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	postSysProxy(t, h, true) // then "crash": no Release
	if err := h.RecoverSystemProxy(false); err != nil {
		t.Fatalf("RecoverSystemProxy: %v", err)
	}
	if f.on || f.disables != 1 || markerExists(h) {
		t.Fatalf("stale system proxy not cleaned up: on=%v disables=%d marker=%v", f.on, f.disables, markerExists(h))
	}
}

func TestSystemProxyKeptAcrossProjectSwitch(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	postSysProxy(t, h, true)
	if err := h.RecoverSystemProxy(true); err != nil {
		t.Fatal(err)
	}
	if !f.on || f.disables != 0 || !markerExists(h) {
		t.Fatalf("project-switch re-exec must keep the proxy: on=%v disables=%d marker=%v", f.on, f.disables, markerExists(h))
	}
}

func TestSystemProxyFailedDisableKeepsMarkerForRetry(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	postSysProxy(t, h, true)
	f.failOff = true
	if err := h.ReleaseSystemProxy(); err == nil {
		t.Fatal("expected disable error")
	}
	if !markerExists(h) {
		t.Fatal("marker must survive a failed disable so the next start retries")
	}
}

// writeForeignMarker records the system proxy as enabled by another process.
func writeForeignMarker(t *testing.T, h *Hub, pid int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.GlobalDir, sysProxyMarkerName), []byte(strconv.Itoa(pid)+" 127.0.0.1:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Projects share the global data dir, so a second instance must not switch off
// the system proxy a still-running first instance enabled — at start or quit.
func TestSystemProxyOwnedByLiveInstanceIsLeftAlone(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	f.on = true
	other := os.Getpid() + 1
	h.sysProxyOwnerAlive = func(pid int) bool { return pid == other }
	writeForeignMarker(t, h, other)
	if err := h.RecoverSystemProxy(false); err != nil {
		t.Fatal(err)
	}
	if err := h.ReleaseSystemProxy(); err != nil {
		t.Fatal(err)
	}
	if !f.on || f.disables != 0 || !markerExists(h) {
		t.Fatalf("another live instance's system proxy was touched: on=%v disables=%d marker=%v", f.on, f.disables, markerExists(h))
	}
}

func TestSystemProxyOwnedByDeadInstanceIsRecovered(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	f.on = true
	h.sysProxyOwnerAlive = func(int) bool { return false }
	writeForeignMarker(t, h, os.Getpid()+1)
	if err := h.RecoverSystemProxy(false); err != nil {
		t.Fatal(err)
	}
	if f.on || f.disables != 1 || markerExists(h) {
		t.Fatalf("dead instance's system proxy not recovered: on=%v disables=%d marker=%v", f.on, f.disables, markerExists(h))
	}
}

// A project switch on Windows re-spawns with a new pid: the new process adopts
// the marker so quitting it still turns the proxy off.
func TestSystemProxyProjectSwitchAdoptsMarker(t *testing.T) {
	h, _, _ := newHub(t)
	f := installFakeSysProxy(t, h)
	f.on = true
	old := os.Getpid() + 1
	h.sysProxyOwnerAlive = func(pid int) bool { return pid == old }
	writeForeignMarker(t, h, old)
	if err := h.RecoverSystemProxy(true); err != nil {
		t.Fatal(err)
	}
	if err := h.ReleaseSystemProxy(); err != nil {
		t.Fatal(err)
	}
	if f.on || f.disables != 1 {
		t.Fatalf("adopted system proxy not released on quit: on=%v disables=%d", f.on, f.disables)
	}
}
