package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func authzPost(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return 0, nil
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func authzNames(t *testing.T, h *Hub) []string {
	t.Helper()
	ids, err := (&authzAPI{h}).authzIdentitiesResult()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, id := range ids {
		names = append(names, id.Name)
	}
	return names
}

func TestSetAuthzDefaultsToMergeByName(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer a1"}]}`)
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"user","headers":"Authorization: Bearer u1"}]}`)
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer a2"}],"owner":"agent-b"}`)

	ids, _ := (&authzAPI{h}).authzIdentitiesResult()
	if len(ids) != 2 {
		t.Fatalf("identities = %+v, want admin+user preserved", ids)
	}
	for _, id := range ids {
		if id.UpdatedAt == "" {
			t.Errorf("%s missing updatedAt", id.Name)
		}
		if id.Name == "admin" && (!strings.Contains(id.Headers, "a2") || id.Owner != "agent-b") {
			t.Errorf("admin not updated in place: %+v", id)
		}
	}
}

func TestSetAuthzReplaceModeReplacesList(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"X-A: 1"},{"name":"user","headers":"X-A: 2"}]}`)
	authzPost(t, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"only","headers":"X-A: 3"}]}`)
	if got := authzNames(t, h); len(got) != 1 || got[0] != "only" {
		t.Fatalf("names = %v, want [only]", got)
	}
	code, _ := authzPost(t, ts.URL+"/api/authz", `{"mode":"bogus","identities":[]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("bogus mode status = %d, want 400", code)
	}
}

func TestAddRemoveListAuthzIdentity(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	code, _ := authzPost(t, ts.URL+"/api/authz/identity", `{"name":"admin","headers":{"Authorization":"Bearer x"},"owner":"a"}`)
	if code != http.StatusOK {
		t.Fatalf("add status = %d", code)
	}
	authzPost(t, ts.URL+"/api/authz/identity", `{"name":"user","headers":"X-A: 1"}`)
	code, _ = authzPost(t, ts.URL+"/api/authz/identity", `{"headers":"X-A: 1"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("nameless add status = %d, want 400", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/authz/identity/admin", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/authz/identity/ghost", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("remove missing status = %d, want 404", resp.StatusCode)
	}
	if got := authzNames(t, h); len(got) != 1 || got[0] != "user" {
		t.Fatalf("names = %v, want [user]", got)
	}
}

func TestSetAuthzConcurrentMergesDoNotClobber(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i))
			authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"`+name+`","headers":"X-A: 1"}]}`)
			authzPost(t, ts.URL+"/api/authz/identity", `{"name":"`+name+`2","headers":"X-A: 1"}`)
		}(i)
	}
	wg.Wait()
	if got := authzNames(t, h); len(got) != 2*n {
		t.Fatalf("got %d identities (%v), want %d", len(got), got, 2*n)
	}
}
