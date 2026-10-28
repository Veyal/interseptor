package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// update_rule must change only the supplied fields: PUT replaces the whole rule,
// so omitted args used to reset enabled/ord/replace/bigBody to defaults.
func TestUpdateRuleMergesOnlySuppliedArgs(t *testing.T) {
	var put map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/rules":
			io.WriteString(w, `{"rules":[{"id":2,"ord":1,"enabled":true,"type":"req-header","match":"x","replace":"y"},{"id":3,"ord":7,"enabled":false,"type":"res-body","match":"old","replace":"keep","bigBody":true}]}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/rules/3":
			json.NewDecoder(r.Body).Decode(&put)
			io.WriteString(w, `{"ok":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("update_rule", map[string]any{"id": 3, "match": "new"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"ord": float64(7), "enabled": false, "type": "res-body", "match": "new", "replace": "keep", "bigBody": true}
	for k, v := range want {
		if put[k] != v {
			t.Fatalf("PUT %s = %#v, want %#v (body %#v)", k, put[k], v, put)
		}
	}

	// side alone keeps the existing kind; type alone keeps the existing side.
	if _, err := s.Call("update_rule", map[string]any{"id": 3, "side": "request"}); err != nil {
		t.Fatal(err)
	}
	if put["type"] != "req-body" {
		t.Fatalf("side-only update type = %#v, want req-body", put["type"])
	}
	if _, err := s.Call("update_rule", map[string]any{"id": 3, "type": "header", "enabled": "true"}); err != nil {
		t.Fatal(err)
	}
	if put["type"] != "res-header" || put["enabled"] != true {
		t.Fatalf("type-only update = %#v", put)
	}

	if _, err := s.Call("update_rule", map[string]any{"id": 99, "match": "z"}); err == nil {
		t.Fatal("updating a missing rule should fail")
	}
}
