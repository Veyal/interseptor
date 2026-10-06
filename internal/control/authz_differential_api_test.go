package control

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestAuthzDifferentialEndpointValidatesAndAttaches(t *testing.T) {
	var state atomic.Int64
	_, f := differentialTarget(t, &state)
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	flowID, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := st.CreateFinding(&store.Finding{Title: "differential"})
	if err != nil {
		t.Fatal(err)
	}
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"}]}`)

	if code, _ := authzPost(t, ts.URL+"/api/authz/differential", `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing flowId status = %d, want 400", code)
	}
	if code, _ := authzPost(t, ts.URL+"/api/authz/differential", `{"flowId":`+itoa64(flowID)+`,"identities":["ghost"]}`); code != http.StatusBadRequest {
		t.Fatalf("unknown identity status = %d, want 400", code)
	}
	code, out := authzPost(t, ts.URL+"/api/authz/differential", `{"flowId":`+itoa64(flowID)+`,"attachToFinding":`+itoa64(fid)+`}`)
	if code != http.StatusOK || out["kind"] != "authz_differential" {
		t.Fatalf("status = %d body = %v", code, out)
	}
	got, err := st.GetFinding(fid)
	if err != nil || len(got.Flows) != 2 {
		t.Fatalf("attached flows = %d (err %v), want 2 (admin + anonymous)", len(got.Flows), err)
	}
}
