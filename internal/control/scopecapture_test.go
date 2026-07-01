package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PUT /api/settings {captureScopeOnly:true} persists the choice, calls the wired
// proxy hook, and GET /api/settings reflects it.
func TestCaptureScopeOnlySetting(t *testing.T) {
	h, _, _ := newHub(t)
	var got, called bool
	h.SetCaptureScopeOnly = func(v bool) { got, called = v, true }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"captureScopeOnly": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	if !called || !got {
		t.Fatalf("SetCaptureScopeOnly called=%v got=%v, want true/true", called, got)
	}

	gresp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()
	var s map[string]any
	json.NewDecoder(gresp.Body).Decode(&s)
	if s["captureScopeOnly"] != true {
		t.Fatalf("getSettings captureScopeOnly = %v, want true", s["captureScopeOnly"])
	}
}

// PUT /api/settings {invisibleProxy:true} persists the choice, calls the wired
// proxy hook, and GET /api/settings reflects it.
func TestInvisibleProxySetting(t *testing.T) {
	h, _, _ := newHub(t)
	var got, called bool
	h.SetInvisibleProxy = func(v bool) { got, called = v, true }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"invisibleProxy": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	if !called || !got {
		t.Fatalf("SetInvisibleProxy called=%v got=%v, want true/true", called, got)
	}

	gresp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()
	var s map[string]any
	json.NewDecoder(gresp.Body).Decode(&s)
	if s["invisibleProxy"] != true {
		t.Fatalf("getSettings invisibleProxy = %v, want true", s["invisibleProxy"])
	}
}
