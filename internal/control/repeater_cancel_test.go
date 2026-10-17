package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// hangingOrigin accepts requests and never answers them. disconnected receives
// one value per request whose client connection went away, which is how the
// tests observe that Interseptor tore down the upstream send.
type hangingOrigin struct {
	server       *httptest.Server
	disconnected chan struct{}
	release      chan struct{}
	unblock      func()
}

func newHangingOrigin(t *testing.T) *hangingOrigin {
	t.Helper()
	o := &hangingOrigin{disconnected: make(chan struct{}, 16), release: make(chan struct{})}
	o.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			o.disconnected <- struct{}{}
		case <-o.release:
		}
	}))
	o.unblock = sync.OnceFunc(func() { close(o.release) })
	t.Cleanup(func() {
		o.unblock()
		o.server.Close()
	})
	return o
}

func (o *hangingOrigin) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portStr, _ := strings.Cut(strings.TrimPrefix(o.server.URL, "http://"), ":")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("origin port: %v", err)
	}
	return host, port
}

// abandon posts body to url with a client that gives up quickly, like a
// browser tab that is closed or a Repeater Cancel click.
func abandon(t *testing.T, url, body string) {
	t.Helper()
	c := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err == nil {
		resp.Body.Close()
		t.Fatalf("POST %s answered %d before the hanging origin replied", url, resp.StatusCode)
	}
}

func (o *hangingOrigin) wantDisconnect(t *testing.T, what string) {
	t.Helper()
	select {
	case <-o.disconnected:
	case <-time.After(5 * time.Second):
		o.unblock() // let the stuck handler finish so the test fails instead of hanging
		t.Fatalf("%s: the upstream send kept running after the client went away", what)
	}
}

func TestRepeaterSendStopsWhenClientCancels(t *testing.T) {
	origin := newHangingOrigin(t)
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	abandon(t, ts.URL+"/api/repeater/send", `{"method":"GET","url":"`+origin.server.URL+`/slow"}`)
	origin.wantDisconnect(t, "repeater send")
}

func TestReplayFlowStopsWhenClientCancels(t *testing.T) {
	origin := newHangingOrigin(t)
	h, s, _ := newHub(t)
	host, port := origin.hostPort(t)
	id, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "http", Host: host, Port: port, Path: "/slow"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	abandon(t, fmt.Sprintf("%s/api/flows/%d/replay", ts.URL, id), `{}`)
	origin.wantDisconnect(t, "flow replay")
}

func TestFindingRetestStopsWhenClientCancels(t *testing.T) {
	origin := newHangingOrigin(t)
	h, s, _ := newHub(t)
	host, port := origin.hostPort(t)
	var flowIDs []int64
	for i := 0; i < 3; i++ {
		id, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "http", Host: host, Port: port, Path: "/slow/" + strconv.Itoa(i), Status: 200})
		if err != nil {
			t.Fatal(err)
		}
		flowIDs = append(flowIDs, id)
	}
	fid, err := s.CreateFinding(&store.Finding{Severity: "High", Status: "verified", Title: "Slow retest", Target: origin.server.URL}, store.FindingChange{Actor: "test", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range flowIDs {
		if err := s.AttachFlowWithMetadata(fid, id, "evidence", -1, "action", "", "captured_flow", 0, store.FindingChange{Actor: "test", Source: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	abandon(t, fmt.Sprintf("%s/api/findings/%d/retest", ts.URL, fid), `{}`)
	origin.wantDisconnect(t, "finding retest")
	// The remaining evidence flows must not be replayed once the caller is gone.
	time.Sleep(300 * time.Millisecond)
	select {
	case <-origin.disconnected:
		t.Fatal("finding retest kept replaying evidence after the client went away")
	default:
	}
	f, err := s.GetFinding(fid)
	if err != nil {
		t.Fatal(err)
	}
	for _, bl := range f.Blocks {
		if bl.Role == "retest" {
			t.Fatalf("an aborted retest attached flow %d as retest evidence", bl.FlowID)
		}
	}
}

// A send whose caller sets no timeout still ends: the control plane applies a
// generous default so a hung origin cannot pin a handler forever.
func TestRepeaterSendAppliesDefaultTimeout(t *testing.T) {
	origin := newHangingOrigin(t)
	prev := defaultSendTimeout
	defaultSendTimeout = 200 * time.Millisecond
	t.Cleanup(func() { defaultSendTimeout = prev })
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	start := time.Now()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(`{"method":"GET","url":"`+origin.server.URL+`/slow"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("default timeout not applied: took %s", elapsed)
	}
	if out["status"] != float64(http.StatusBadGateway) || !strings.Contains(fmt.Sprint(out["error"]), "deadline") {
		t.Fatalf("want a 502 flow that names the deadline, got status=%v error=%v", out["status"], out["error"])
	}

	// An explicit timeoutMs wins over the default, in both directions.
	defaultSendTimeout = 5 * time.Second
	start = time.Now()
	postJSONDecode(t, ts.URL+"/api/repeater/send", `{"method":"GET","url":"`+origin.server.URL+`/slow","timeoutMs":150}`, &out)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("explicit timeoutMs ignored: took %s", elapsed)
	}
}
