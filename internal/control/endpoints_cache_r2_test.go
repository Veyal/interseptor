package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func endpointHosts(t *testing.T, h *Hub) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	(&flowAPI{h}).listEndpoints(rec, httptest.NewRequest(http.MethodGet, "/api/endpoints", nil))
	var out struct {
		Endpoints []struct {
			Host string `json:"host"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode endpoints: %v (%s)", err, rec.Body.String())
	}
	hosts := make([]string, 0, len(out.Endpoints))
	for _, e := range out.Endpoints {
		hosts = append(hosts, e.Host)
	}
	return hosts
}

// C10: listEndpoints aggregates outside the cache lock. A change that invalidated
// the cache while the aggregation was running was followed by a cache write of the
// pre-change result, which then stayed until the next invalidation.
func TestEndpointsCacheDropsResultComputedBeforeInvalidation(t *testing.T) {
	h, st, _ := newHub(t)
	insert := func(host string) {
		t.Helper()
		if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: host, Port: 443, Path: "/", HTTPVersion: "HTTP/1.1", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	insert("first.example.com")

	// Interleave: the aggregation has read the store; before its result is cached
	// an import adds a flow and invalidates the cache.
	h.epsCache.afterQuery = func() {
		h.epsCache.afterQuery = nil
		insert("second.example.com")
		h.epsCache.invalidate()
	}
	if got := endpointHosts(t, h); len(got) != 1 {
		t.Fatalf("the racing request itself returns the pre-change result: %v", got)
	}
	if got := endpointHosts(t, h); len(got) != 2 {
		t.Fatalf("after the invalidation the endpoints list is %v, want both hosts: the stale pre-invalidation result was cached", got)
	}
}

// Without a racing invalidation the result is cached as before.
func TestEndpointsCacheStillCachesWithoutInvalidation(t *testing.T) {
	var c endpointsCache
	gen := c.generation()
	if !c.setAt(gen, "k", []store.Endpoint{{Host: "a.example.com"}}, "", 1, false) {
		t.Fatal("setAt refused a result computed at the current generation")
	}
	if _, _, _, _, ok := c.get("k"); !ok {
		t.Fatal("entry not cached")
	}
	c.invalidate()
	if c.setAt(gen, "k", nil, "", 0, false) {
		t.Fatal("setAt stored a result from before the invalidation")
	}
	if _, _, _, _, ok := c.get("k"); ok {
		t.Fatal("stale entry cached after invalidation")
	}
}
