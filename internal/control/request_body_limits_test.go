package control

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Source-management endpoints have a tighter limit than the control plane's
// global request cap. A valid JSON value followed by padding must not bypass
// that endpoint limit merely because json.Decoder stops after the first value.
func TestSourceEndpointsRejectOversizedJSONAfterValidValue(t *testing.T) {
	h, _, _ := newHub(t)
	h.ChecksDir = t.TempDir()
	h.ActiveChecksDir = t.TempDir()
	h.ProjectDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	passive := `{"source":"def check(flow):\n    return []\n"}`
	active := `{"source":"def check(point, baseline, probe):\n    return []\n"}`

	tests := []struct {
		name   string
		method string
		path   string
		prefix string
	}{
		{name: "checks disabled", method: http.MethodPut, path: "/api/checks/disabled", prefix: `{"disabled":[]}`},
		{name: "save passive check", method: http.MethodPut, path: "/api/checks/limit-test", prefix: passive},
		{name: "test passive check", method: http.MethodPost, path: "/api/checks/test", prefix: passive},
		{name: "save active check", method: http.MethodPut, path: "/api/active-checks/limit-test", prefix: active},
		{name: "test active check", method: http.MethodPost, path: "/api/active-checks/test", prefix: active},
		{name: "save codec", method: http.MethodPut, path: "/api/codecs/limit-test", prefix: `{}`},
		{name: "test codec", method: http.MethodPost, path: "/api/codecs/test", prefix: `{}`},
		{name: "encode codec", method: http.MethodPost, path: "/api/codecs/encode", prefix: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.prefix + strings.Repeat(" ", maxCheckSource+1)
			req, err := http.NewRequest(tt.method, ts.URL+tt.path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

func TestOtherBoundedJSONEndpointsRejectOversizedPadding(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	tests := []struct {
		name   string
		path   string
		prefix string
		limit  int
	}{
		{
			name:   "saved flow search",
			path:   "/api/flow-searches/test",
			prefix: `{"name":"bounded","scope":"anywhere","script":"def match(flow): return True"}`,
			limit:  maxFlowSearchRequestBytes,
		},
		{
			name:   "IP allowlist",
			path:   "/api/allowlist",
			prefix: `{"cidr":"192.0.2.0/24","label":"example network"}`,
			limit:  maxAllowlistRequestBytes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+tt.path,
				strings.NewReader(tt.prefix+strings.Repeat(" ", tt.limit+1)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

func TestCredentialAndPeerJSONEndpointsRejectOversizedPadding(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	if err := h.saveVaultClient(vaultClientConfig{URL: "http://127.0.0.1:1", Key: "example-key"}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		limit  int
		prefix string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{name: "browser session login", limit: maxSessionLoginRequestBytes, prefix: `{"token":"invalid-example-token"}`, call: h.sessionLogin},
		{name: "vault config", limit: maxVaultClientRequestBytes, prefix: `{"url":"http://127.0.0.1:1","key":"example-key"}`, call: h.putVaultConfig},
		{name: "vault backup", limit: maxVaultClientRequestBytes, prefix: `{"id":"bad!"}`, call: h.vaultBackup},
		{name: "vault import", limit: maxVaultClientRequestBytes, prefix: `{"id":"bad!"}`, call: h.vaultImport},
		{name: "vault merge", limit: maxVaultClientRequestBytes, prefix: `{"id":"bad!"}`, call: h.vaultMerge},
		{name: "peer pull", limit: maxMergeRequestBytes, prefix: `{"peerUrl":"://","key":"example-key"}`, call: h.mergePull},
		{name: "peer push", limit: maxMergeRequestBytes, prefix: `{"peerUrl":"http://127.0.0.1:1","key":"example-key","dryRun":true}`, call: h.mergePush},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.prefix+strings.Repeat(" ", tt.limit+1)))
			req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
			rec := httptest.NewRecorder()
			tt.call(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
			}
		})
	}
}

func TestUIStateRejectsOversizedJSONPadding(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPut, "/api/ui/repeater",
		strings.NewReader(`{}`+strings.Repeat(" ", maxUIStateBytes+1)))
	req.SetPathValue("panel", "repeater")
	rec := httptest.NewRecorder()
	h.putUIState(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}
