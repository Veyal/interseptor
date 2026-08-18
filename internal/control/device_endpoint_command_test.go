package control

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceEndpointRejectsTrailingJSONBeforeChangingSettings(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSettings(map[string]string{
		settingDeviceProxyMode: "auto",
		settingDeviceProxyHost: "old.example.com",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/proxy/device-endpoint", "application/json",
		strings.NewReader(`{"mode":"manual","host":"192.0.2.10"}{}`))
	if err != nil {
		t.Fatalf("POST device endpoint: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	assertDeviceProxySettings(t, st, "auto", "old.example.com")
}

func TestDeviceEndpointSettingsRollBackTogether(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSettings(map[string]string{
		settingDeviceProxyMode: "auto",
		settingDeviceProxyHost: "old.example.com",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db"))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_device_host BEFORE INSERT ON settings
		WHEN NEW.key = 'proxy.deviceHost'
		BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		db.Close()
		t.Fatalf("create trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close project db: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/proxy/device-endpoint", "application/json",
		strings.NewReader(`{"mode":"manual","host":"192.0.2.10"}`))
	if err != nil {
		t.Fatalf("POST device endpoint: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	assertDeviceProxySettings(t, st, "auto", "old.example.com")
}

func assertDeviceProxySettings(t *testing.T, st interface {
	GetSetting(string) (string, bool, error)
}, wantMode, wantHost string) {
	t.Helper()
	mode, modeOK, err := st.GetSetting(settingDeviceProxyMode)
	if err != nil {
		t.Fatalf("read device mode: %v", err)
	}
	host, hostOK, err := st.GetSetting(settingDeviceProxyHost)
	if err != nil {
		t.Fatalf("read device host: %v", err)
	}
	if !modeOK || mode != wantMode || !hostOK || host != wantHost {
		t.Fatalf("device settings = mode %q (ok=%v), host %q (ok=%v); want %q, %q", mode, modeOK, host, hostOK, wantMode, wantHost)
	}
}
