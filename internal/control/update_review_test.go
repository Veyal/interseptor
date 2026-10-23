package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/version"
)

// Only the local operator (unauthenticated loopback: loopback connection and
// loopback Host) may replace and restart the tool. A collaborator signed in
// through a tunnel arrives on loopback too, but with the tunnel's Host.
func TestUpdateLocalOperatorOnly(t *testing.T) {
	loop := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9966}
	req := func(host string, local net.Addr) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/update", nil)
		r.Host = host
		if local != nil {
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
		}
		return r
	}
	if !localOperator(req("127.0.0.1:9966", loop)) || !localOperator(req("localhost:9966", loop)) {
		t.Fatal("the local operator must be allowed")
	}
	for name, r := range map[string]*http.Request{
		"tunnel host":      req("mini.example.ts.net", loop),
		"lan connection":   req("127.0.0.1:9966", &net.TCPAddr{IP: net.ParseIP("192.0.2.5"), Port: 9966}),
		"no local address": req("127.0.0.1:9966", nil),
	} {
		if localOperator(r) {
			t.Errorf("%s: must not be treated as the local operator", name)
		}
	}
}

// The badge hands off to the native app only when this server is the one the
// macOS app runs; /api/version says so.
func TestVersionReportsInstallMethod(t *testing.T) {
	f := &fakeUpdater{install: version.InstallInfo{Method: version.InstallDesktop}, latest: newerRelease}
	_, ts, _ := newUpdateHub(t, f)
	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["installMethod"] != "desktop" {
		t.Fatalf("installMethod = %v, want desktop", out["installMethod"])
	}
}
