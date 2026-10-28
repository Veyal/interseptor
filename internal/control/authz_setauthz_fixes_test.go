package control

import (
	"net/http/httptest"
	"testing"
)

func authzIdentityNamed(t *testing.T, h *Hub, name string) identity {
	t.Helper()
	ids, err := (&authzAPI{h}).authzIdentitiesResult()
	if err != nil {
		t.Fatal(err)
	}
	id, ok := findIdentity(ids, name)
	if !ok {
		t.Fatalf("identity %q missing from %+v", name, ids)
	}
	return id
}

func eventKinds(h *Hub, name string) []string {
	var out []string
	for _, ev := range (&authzAPI{h}).identityEventsFor(name) {
		out = append(out, ev.Kind)
	}
	return out
}

// The legacy modal sends {name, headers, broken} in replace mode; a still
// broken identity must keep its broken reason.
func TestSetAuthzReplaceKeepsBrokenNote(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"user","headers":"X-A: 1","broken":true,"brokenNote":"locked after rate-limit test"}]}`)
	authzPost(t, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"user","headers":"X-A: 1","broken":true}]}`)
	if got := authzIdentityNamed(t, h, "user"); !got.Broken || got.BrokenNote != "locked after rate-limit test" {
		t.Fatalf("broken note lost: %+v", got)
	}
	// Un-breaking clears the reason.
	authzPost(t, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"user","headers":"X-A: 1"}]}`)
	if got := authzIdentityNamed(t, h, "user"); got.Broken || got.BrokenNote != "" {
		t.Fatalf("unbroken identity kept note: %+v", got)
	}
}

// Replace mode removes identities missing from the list; their event logs go
// with them so a later identity reusing the name starts fresh.
func TestSetAuthzReplaceDropsRemovedIdentityEvents(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"old","headers":"X-A: 1"},{"name":"keep","headers":"X-A: 2"}]}`)
	authzPost(t, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"keep","headers":"X-A: 2"}]}`)
	if got := eventKinds(h, "old"); len(got) != 0 {
		t.Fatalf("removed identity kept events %v", got)
	}
	if got := eventKinds(h, "keep"); len(got) == 0 {
		t.Fatal("kept identity lost its events")
	}
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"old","headers":"X-A: 9"}]}`)
	if got := eventKinds(h, "old"); len(got) != 1 || got[0] != identityEventCreated {
		t.Fatalf("recreated identity inherited timeline %v", got)
	}
}

// A legacy Run re-saves every identity; unchanged ones keep their owner and
// updatedAt and log no "updated" event.
func TestSetAuthzReplaceSkipsUnchangedIdentities(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"X-A: 1","role":"admin"},{"name":"user","headers":"X-A: 2"}],"owner":"agent-a"}`)
	before := authzIdentityNamed(t, h, "admin")
	authzPost(t, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"admin","headers":"X-A: 1"},{"name":"user","headers":"X-A: 3"}],"owner":"agent-b"}`)
	after := authzIdentityNamed(t, h, "admin")
	if after.Owner != "agent-a" || after.UpdatedAt != before.UpdatedAt || after.Role != "admin" {
		t.Fatalf("unchanged identity restamped: before=%+v after=%+v", before, after)
	}
	if got := eventKinds(h, "admin"); len(got) != 1 {
		t.Fatalf("unchanged identity logged events %v", got)
	}
	if u := authzIdentityNamed(t, h, "user"); u.Owner != "agent-b" {
		t.Fatalf("changed identity not restamped: %+v", u)
	}
	if got := eventKinds(h, "user"); len(got) != 2 || got[1] != identityEventUpdated {
		t.Fatalf("changed identity events %v", got)
	}
}

// Duplicate names in one payload create one identity and one "created" event.
func TestSetAuthzDuplicateNamesLogOneCreate(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"dup","headers":"X-A: 1"},{"name":"dup","headers":"X-A: 2"}]}`)
	if got := authzNames(t, h); len(got) != 1 {
		t.Fatalf("names = %v", got)
	}
	created := 0
	for _, k := range eventKinds(h, "dup") {
		if k == identityEventCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("dup created events = %d (%v)", created, eventKinds(h, "dup"))
	}
}
