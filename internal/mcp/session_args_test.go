package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// set_session used argStr on headers, so an agent passing a header object (the
// shape send_request accepts) stored the Go fmt.Sprint of the map:
// "map[Authorization:Bearer example-token]". Objects must become header lines.
func TestSetSessionAcceptsHeaderObjects(t *testing.T) {
	var got map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer mock.Close()
	s := New(mock.URL)

	_, err := s.Call("set_session", map[string]any{
		"enabled": true,
		"headers": map[string]any{"Authorization": "Bearer example-token"},
		"hostHeaders": map[string]any{
			"api.example.com": map[string]any{"Cookie": "sid=example"},
			"www.example.com": "X-Token: example",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := got["headers"].(string); h != "Authorization: Bearer example-token" {
		t.Fatalf("headers = %q, want header lines", got["headers"])
	}
	hh, _ := got["hostHeaders"].(map[string]any)
	if v, _ := hh["api.example.com"].(string); v != "Cookie: sid=example" {
		t.Fatalf("hostHeaders[api] = %#v, want header lines", hh["api.example.com"])
	}
	if v, _ := hh["www.example.com"].(string); v != "X-Token: example" {
		t.Fatalf("hostHeaders[www] = %#v", hh["www.example.com"])
	}
	if strings.Contains(string(mustJSON(got)), "map[") {
		t.Fatalf("Go map formatting leaked into the session: %s", mustJSON(got))
	}
}

func TestSetSessionRejectsMalformedHostHeaders(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("a malformed hostHeaders must not reach the control API")
	}))
	defer mock.Close()
	s := New(mock.URL)
	if _, err := s.Call("set_session", map[string]any{"enabled": true, "hostHeaders": "api.example.com"}); err == nil {
		t.Fatal("hostHeaders as a bare string must be rejected")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
