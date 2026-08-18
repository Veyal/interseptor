package control

import (
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
