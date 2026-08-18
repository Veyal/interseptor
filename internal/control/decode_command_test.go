package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeUtilitiesRejectTrailingJSON(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "decode", path: "/api/decode", body: `{"op":"base64decode","input":"aGVsbG8="}{}`},
		{name: "selection decode", path: "/api/selection-decode", body: `{"input":"aGVsbG8="}{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
