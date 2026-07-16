package control_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestRetentionPolicyMaxFlows(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := int64(0); i < 5; i++ {
		if _, err := st.InsertFlow(&store.Flow{Method: "GET", Host: "h.test", Path: "/x", TS: time.UnixMilli(1_000_000 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	hub := control.New(st, intercept.New(), nil, nil, nil)
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	set := bytes.NewReader(mustJSON(t, map[string]int64{"maxFlows": 2}))
	resp, err := http.NewRequest(http.MethodPut, srv.URL+"/api/flows/retention", set)
	if err != nil {
		t.Fatal(err)
	}
	resp.Header.Set("content-type", "application/json")
	if r, err := http.DefaultClient.Do(resp); err != nil || r.StatusCode != 200 {
		t.Fatalf("PUT retention: %v %v", r, err)
	}

	run, err := http.Post(srv.URL+"/api/flows/retention/run", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Deleted int64 }
	json.NewDecoder(run.Body).Decode(&out)
	run.Body.Close()
	if out.Deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", out.Deleted)
	}

	// GET hosts/stats confirms 2 flows remain.
	hs, _ := http.Get(srv.URL + "/api/hosts/stats")
	var stats struct{ TotalFlows int64 }
	json.NewDecoder(hs.Body).Decode(&stats)
	hs.Body.Close()
	if stats.TotalFlows != 2 {
		t.Fatalf("expected 2 flows remaining, got %d", stats.TotalFlows)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
