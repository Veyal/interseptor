package control

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

// aiSourceFlag maps the X-Interceptor-Source request header to FlagAI (only the
// MCP server sets it), case-insensitively; anything else contributes no flag.
func TestAISourceFlag(t *testing.T) {
	mk := func(v string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		if v != "" {
			r.Header.Set("X-Interceptor-Source", v)
		}
		return r
	}
	for _, c := range []struct {
		in   string
		want int64
	}{{"ai", store.FlagAI}, {"AI", store.FlagAI}, {"", 0}, {"user", 0}} {
		if got := aiSourceFlag(mk(c.in)); got != c.want {
			t.Errorf("aiSourceFlag(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A Repeater send carrying the X-Interceptor-Source: ai header (i.e. from the AI
// over MCP) is tagged FlagAI and shows up in Proxy/History, while an ordinary
// Repeater send stays hidden there (it has its own view).
func TestRepeaterSendAISourceShowsInHistory(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer target.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	send := func(path string, ai bool) {
		body, _ := json.Marshal(map[string]string{"method": "GET", "url": target.URL + path})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/repeater/send", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if ai {
			req.Header.Set("X-Interceptor-Source", "ai")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("send %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("send %s: status %d", path, resp.StatusCode)
		}
	}
	send("/ai", true)
	send("/plain", false)

	resp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("GET flows: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			Path  string `json:"path"`
			Flags int64  `json:"flags"`
		} `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)

	var aiSeen, plainSeen bool
	var aiFlags int64
	for _, f := range out.Flows {
		switch f.Path {
		case "/ai":
			aiSeen, aiFlags = true, f.Flags
		case "/plain":
			plainSeen = true
		}
	}
	if !aiSeen {
		t.Fatal("AI-tagged Repeater send should appear in Proxy/History")
	}
	if aiFlags&store.FlagAI == 0 {
		t.Fatalf("AI flow should carry FlagAI, got flags=%d", aiFlags)
	}
	if plainSeen {
		t.Fatal("plain Repeater send should stay hidden from Proxy/History")
	}
}

// Repeater/Intruder/MCP sends must emit flow.new over SSE so Proxy History
// refreshes live — same as proxied traffic.
func TestRepeaterSendBroadcastsFlowNewSSE(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		body, _ := json.Marshal(map[string]string{"method": "GET", "url": target.URL + "/live"})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/repeater/send", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Interceptor-Source", "ai")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("send: %v", err)
			return
		}
		r.Body.Close()
	}()

	sc := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "flow.new") && strings.Contains(line, "/live") {
			return
		}
		if time.Now().After(deadline) {
			break
		}
	}
	t.Fatal("did not receive flow.new SSE for Repeater send")
}
