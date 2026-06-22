package control

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/store"
)

func readAll(r io.Reader) string { b, _ := io.ReadAll(r); return string(b) }

func newHub(t *testing.T) (*Hub, *store.Store, *intercept.Engine) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	eng := intercept.New()
	h := New(s, eng, nil, nil)
	return h, s, eng
}

func TestListFlowsJSON(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "x.com", Path: "/a", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "x.com", Path: "/b", Status: 201})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("GET flows: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []map[string]any `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(out.Flows))
	}
	if out.Flows[0]["path"] != "/b" { // newest first
		t.Fatalf("expected newest-first, got %v", out.Flows[0]["path"])
	}
}

func TestFlowRawRequest(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "x.com", Path: "/a",
		HTTPVersion: "HTTP/1.1", Status: 200,
		ReqHeaders: map[string][]string{"Host": {"x.com"}, "Accept": {"*/*"}},
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/raw?side=req")
	if err != nil {
		t.Fatalf("GET raw: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(resp.Body)
	if !strings.HasPrefix(body, "GET /a HTTP/1.1") {
		t.Fatalf("unexpected raw request: %q", body)
	}
	if !strings.Contains(body, "Host: x.com") {
		t.Fatalf("raw request missing Host: %q", body)
	}
}

func TestRuleCreateAndList(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"type":"req-header","match":"User-Agent: .*","replace":"User-Agent: x","enabled":true}`
	resp, err := http.Post(ts.URL+"/api/rules", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST rule: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create rule status: %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/api/rules")
	if err != nil {
		t.Fatalf("GET rules: %v", err)
	}
	defer resp2.Body.Close()
	var out struct {
		Rules []map[string]any `json:"rules"`
	}
	json.NewDecoder(resp2.Body).Decode(&out)
	if len(out.Rules) != 1 || out.Rules[0]["match"] != "User-Agent: .*" {
		t.Fatalf("unexpected rules: %v", out.Rules)
	}
	// Engine should have been refreshed with the new rule (applies to a request).
	r, _ := http.NewRequest("GET", "https://x.com/", nil)
	r.Header.Set("User-Agent", "Go")
	if err := eng.ApplyRules(r); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	if r.Header.Get("User-Agent") != "x" {
		t.Fatalf("engine not refreshed with rule: UA=%q", r.Header.Get("User-Agent"))
	}
}

func TestRejectBadRuleRegex(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/rules", "application/json",
		strings.NewReader(`{"type":"req-header","match":"([","replace":"","enabled":true}`))
	if err != nil {
		t.Fatalf("POST rule: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad regex, got %d", resp.StatusCode)
	}
}

func TestInterceptToggle(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/intercept/toggle", "application/json", strings.NewReader(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	resp.Body.Close()
	if !eng.Enabled() {
		t.Fatal("expected intercept enabled after toggle")
	}
}

func TestSSEReceivesFlowNew(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	// Push flow.new events repeatedly until the stream delivers one.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				h.FlowCaptured(&store.Flow{ID: 7, Method: "GET", Host: "x.com", Path: "/sse"})
			}
		}
	}()

	sc := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "flow.new") {
			return // success
		}
		if time.Now().After(deadline) {
			break
		}
	}
	t.Fatal("did not receive flow.new SSE event")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
