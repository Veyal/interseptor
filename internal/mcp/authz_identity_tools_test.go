package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthzIdentityToolsMirrorREST(t *testing.T) {
	type received struct {
		method string
		path   string
		body   map[string]any
	}
	var got []received
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		item := received{method: r.Method, path: r.URL.EscapedPath()}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&item.body)
		}
		got = append(got, item)
		io.WriteString(w, `{"identities":[]}`)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}
	if props := toolProperties(t, s, "set_authz"); props["mode"] == nil || props["owner"] == nil {
		t.Error("set_authz schema missing mode/owner")
	}
	for _, name := range []string{"list_authz", "add_authz_identity", "remove_authz_identity"} {
		toolProperties(t, s, name)
	}
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"set_authz", map[string]any{"identities": []any{map[string]any{"name": "a"}}, "mode": "replace"}},
		{"list_authz", map[string]any{}},
		{"add_authz_identity", map[string]any{"name": "admin", "headers": "X-A: 1", "owner": "me"}},
		{"remove_authz_identity", map[string]any{"name": "ad min"}},
	}
	for _, c := range calls {
		if _, err := s.Call(c.tool, c.args); err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
	}
	if len(got) != 4 {
		t.Fatalf("got %d REST calls, want 4: %#v", len(got), got)
	}
	if got[0].path != "/api/authz" || got[0].body["mode"] != "replace" {
		t.Errorf("set_authz call = %#v", got[0])
	}
	if got[1].method != http.MethodGet || got[1].path != "/api/authz" {
		t.Errorf("list_authz call = %#v", got[1])
	}
	if got[2].method != http.MethodPost || got[2].path != "/api/authz/identity" || got[2].body["name"] != "admin" || got[2].body["owner"] != "me" {
		t.Errorf("add_authz_identity call = %#v", got[2])
	}
	if got[3].method != http.MethodDelete || got[3].path != "/api/authz/identity/ad%20min" {
		t.Errorf("remove_authz_identity call = %#v", got[3])
	}
}
