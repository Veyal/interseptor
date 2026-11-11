package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func bulkRunFlowIDs(t *testing.T, out map[string]any) []int64 {
	t.Helper()
	runs, _ := out["runs"].([]any)
	ids := make([]int64, 0, len(runs))
	for _, r := range runs {
		m, _ := r.(map[string]any)
		id, _ := m["flowId"].(float64)
		ids = append(ids, int64(id))
	}
	return ids
}

// The bulk run picks in-scope endpoints. The scope filter must run before the
// row limit: with the limit applied first, an in-scope endpoint older than the
// newest few hundred out-of-scope flows was never tested, and the run reported
// "no in-scope endpoints" (or a short list) as if history held nothing else.
func TestAuthzBulkFindsInScopeEndpointBehindOutOfScopeTraffic(t *testing.T) {
	f := authzRunTarget200(t)
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	// Scope by path so the noise can share the target's loopback host.
	h.sc.SetRules([]store.ScopeRule{{ID: 1, Enabled: true, Action: "include", Host: f.Host, Path: "/api/"}})
	wantID, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ { // more than maxFlows*5 = 5 newer out-of-scope flows
		if _, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "cdn.example.com", Port: 443, Path: "/noise/" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"}]}`)

	code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/run", `{"inScope":true,"maxFlows":1}`, nil)
	if code != http.StatusOK {
		t.Fatalf("bulk run status = %d: %+v", code, out)
	}
	if ids := bulkRunFlowIDs(t, out); len(ids) != 1 || ids[0] != wantID {
		t.Fatalf("bulk run tested flows %v, want [%d]", ids, wantID)
	}
}

// The tester's own replays are stored as flows. They must never be picked as
// bulk targets: the newest row for an endpoint was the previous run's replay
// (for the differential's invalid-body probe, a deliberately broken request),
// so the next sweep replayed that instead of the captured request.
func TestAuthzBulkNeverTargetsItsOwnReplays(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	h.sc.SetRules([]store.ScopeRule{{ID: 1, Enabled: true, Action: "include", Host: u.Hostname()}})
	origID, err := st.InsertFlow(&store.Flow{Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/api/orders"})
	if err != nil {
		t.Fatal(err)
	}
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"}]}`)

	if code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/differential",
		`{"flowId":`+strconv.FormatInt(origID, 10)+`,"invalidBody":"BROKEN-PROBE-BODY"}`, nil); code != http.StatusOK {
		t.Fatalf("differential status = %d: %+v", code, out)
	}
	mu.Lock()
	bodies = nil
	mu.Unlock()

	code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/run", `{"inScope":true}`, nil)
	if code != http.StatusOK {
		t.Fatalf("bulk run status = %d: %+v", code, out)
	}
	if ids := bulkRunFlowIDs(t, out); len(ids) != 1 || ids[0] != origID {
		t.Fatalf("bulk run tested flows %v, want only the captured flow [%d]", ids, origID)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, b := range bodies {
		if b == "BROKEN-PROBE-BODY" {
			t.Fatal("bulk run replayed the differential's invalid-body probe as if it were captured traffic")
		}
	}
}

// A cross-host replay sends the reference request to every in-scope host. The
// captured Host header names the reference host; carrying it along made every
// target receive "Host: <reference>", so a shared front end routed each replay
// back to the reference vhost and the run reported its answer for all hosts.
func TestAuthzCrossHostReplaySendsEachTargetItsOwnHost(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{} // path-less: listener address → Host header received
	target := func() *httptest.Server {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[srv.Listener.Addr().String()] = r.Host
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	ref, other := target(), target()
	flowFor := func(srv *httptest.Server, hostHeader string) *store.Flow {
		u, _ := url.Parse(srv.URL)
		port, _ := strconv.Atoi(u.Port())
		return &store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/api/me",
			ReqHeaders: map[string][]string{"Host": {hostHeader}, "authorization": {"Bearer captured"}}}
	}
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	refID, err := st.InsertFlow(flowFor(ref, "ref.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertFlow(flowFor(other, "other.example.com")); err != nil {
		t.Fatal(err)
	}

	code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/cross-host-replay",
		`{"flowId":`+strconv.FormatInt(refID, 10)+`,"jwt":"aaa.bbb.ccc"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("cross-host status = %d: %+v", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("replayed to %d listeners, want 2: %v", len(seen), seen)
	}
	for addr, host := range seen {
		if host != addr {
			t.Errorf("listener %s received Host %q, want its own address", addr, host)
		}
	}
}

// The replayed token must replace the captured credential whatever the case of
// the captured header name; otherwise two Authorization headers go out.
func TestCrossHostHeadersReplaceCapturedAuthorization(t *testing.T) {
	base := map[string][]string{"Host": {"ref.example.com"}, "authorization": {"Bearer captured"}, "Accept": {"*/*"}}
	got := crossHostHeaders(base, "bearer", "aaa.bbb.ccc")
	var auth []string
	for k, vs := range got {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			auth = append(auth, vs...)
		}
		if http.CanonicalHeaderKey(k) == "Host" {
			t.Errorf("captured Host header kept: %v", vs)
		}
	}
	if len(auth) != 1 || auth[0] != "Bearer aaa.bbb.ccc" {
		t.Errorf("Authorization values = %v, want exactly the replayed token", auth)
	}
	if got["Accept"] == nil || len(base["authorization"]) != 1 || base["Host"] == nil {
		t.Errorf("other headers lost or the captured map was mutated: %v / %v", got, base)
	}
	for k := range crossHostHeaders(base, "path", "aaa.bbb.ccc") {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			t.Error("path mode must send no Authorization header")
		}
	}
}
