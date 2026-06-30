package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

func TestPathSegments(t *testing.T) {
	got := pathSegments("/api/v1/users?id=1")
	if len(got) != 3 || got[0] != "api" || got[2] != "users" {
		t.Fatalf("segments: %v", got)
	}
}

func TestCollectPathSeeds(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _ = st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "app.test", Path: "/admin/dashboard"})
	_, _ = st.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/api/v1/health"})
	h := &discoveryAPI{&Hub{st: st}}
	seeds := h.collectPathSeeds("app.test")
	if len(seeds) < 4 {
		t.Fatalf("expected several seeds, got %v", seeds)
	}
}

func TestDiscoveryScopeTargets(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _ = st.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "acme.com", Enabled: true})
	h := &discoveryAPI{&Hub{st: st}}
	ts := httptest.NewServer(http.HandlerFunc(h.discoveryScopeTargets))
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
