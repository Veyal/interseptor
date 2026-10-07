package control

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// runnerFixture is a collection with a script that writes an environment
// variable (trusted with vars.write) and a loopback target put in scope.
func (f *collFixture) runnerFixture(requests int) (collUID, envUID string) {
	f.t.Helper()
	u, _ := url.Parse(f.target.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Runner"}, asUI, 201, &co)
	events, _ := json.Marshal([]map[string]any{{"listen": "test", "script": map[string]any{"exec": []string{
		"pm.test('ok', function(){ pm.response.to.have.status(200); });",
		"pm.environment.set('seen', 'yes');"}}}})
	for i := 0; i < requests; i++ {
		f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "r" + string(rune('a'+i)),
			"method": "GET", "url": f.target.URL + "/p" + string(rune('a'+i)), "events": json.RawMessage(events)}, asUI, 201, nil)
	}
	f.must("POST", "/api/collections/"+co.UID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead, CapVarsWrite}}, asUI, 200, nil)
	return co.UID, f.newEnv("tok-value-123456")
}

func (f *collFixture) waitRun(runUID string, cond func(p collrun.Progress) bool) collrun.Progress {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var p collrun.Progress
	for time.Now().Before(deadline) {
		f.must("GET", "/api/runner/runs/"+runUID, nil, asUI, 200, &p)
		if cond(p) {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("timed out; last progress %+v", p)
	return p
}

func TestAsyncRunStartStatusAndPersistedResults(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(2)
	var start struct {
		RunUID  string `json:"runUid"`
		Planned int    `json:"plannedSteps"`
	}
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "persist": "discard"}, asUI, 202, &start)
	if start.RunUID == "" || start.Planned != 2 {
		t.Fatalf("start = %+v", start)
	}
	p := f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
	if p.Status != collrun.StatusDone || p.Count != 2 || p.Totals.Sent != 2 || p.Totals.Pass != 2 {
		t.Fatalf("progress = %+v", p)
	}
	if tail := f.waitRun(start.RunUID, func(collrun.Progress) bool { return true }); tail.Report == nil {
		t.Fatal("a finished run must expose its report")
	}
	var since collrun.Progress
	f.must("GET", "/api/runner/runs/"+start.RunUID+"?since=1", nil, asUI, 200, &since)
	if since.Since != 1 || len(since.Items) != 1 {
		t.Fatalf("since offset = %+v", since)
	}
	var rows struct{ Results []store.CollRunResult }
	f.must("GET", "/api/runs/"+start.RunUID, nil, asUI, 200, &rows)
	if len(rows.Results) != 2 {
		t.Fatalf("persisted rows = %d", len(rows.Results))
	}
	// discard: the script's write did not become a current value.
	var vars struct{ Variables []varView }
	f.must("GET", "/api/variables/environment/"+envUID, nil, asUI, 200, &vars)
	for _, v := range vars.Variables {
		if v.Key == "seen" {
			t.Fatalf("discarded run wrote %s", v.Key)
		}
	}
	f.must("GET", "/api/runner/runs/nope", nil, asUI, 404, nil)
}

func TestAsyncRunEventStreamCarriesStartItemsAndDone(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(2)
	var start struct {
		RunUID string `json:"runUid"`
	}
	// Delay between requests so the stream attaches mid-run.
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "persist": "discard", "delayMs": 300}, asUI, 202, &start)
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/runner/runs/"+start.RunUID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}
	var types []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) == nil {
			types = append(types, e.Type)
		}
		if e.Type == "done" {
			break
		}
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "item") || !strings.HasSuffix(joined, "done") || types[0] != "snapshot" {
		t.Fatalf("event types = %v", types)
	}
}

func TestAsyncRunPauseResumeAbort(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(3)
	var start struct {
		RunUID string `json:"runUid"`
	}
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "persist": "discard", "delayMs": 400}, asUI, 202, &start)
	base := "/api/runner/runs/" + start.RunUID
	f.must("POST", base+"/pause", nil, asUI, 200, nil)
	f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Status == collrun.LivePaused })
	f.must("POST", base+"/resume", nil, asUI, 200, nil)
	f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Status == collrun.StatusRunning && p.Count >= 1 })
	f.must("POST", base+"/abort", nil, asUI, 200, nil)
	p := f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
	if p.Status != collrun.StatusAborted || p.Count >= 3 {
		t.Fatalf("abort: %+v", p)
	}
}

func TestAsyncRunPersistAskHandshake(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(1)
	var start struct {
		RunUID string `json:"runUid"`
	}
	// The UI default is ask.
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID}, asUI, 202, &start)
	base := "/api/runner/runs/" + start.RunUID
	p := f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Status == collrun.LiveAwaitingPersist })
	if len(p.Pending) != 1 || p.Pending[0].Key != "seen" {
		t.Fatalf("pending writes = %+v", p.Pending)
	}
	// Variable persistence is a human decision.
	f.must("POST", base+"/persist", map[string]any{"keep": true}, asAI, 403, nil)
	f.must("POST", base+"/persist", map[string]any{"keep": true}, asUI, 200, nil)
	f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
	var vars struct{ Variables []varView }
	f.must("GET", "/api/variables/environment/"+envUID, nil, asUI, 200, &vars)
	kept := false
	for _, v := range vars.Variables {
		kept = kept || (v.Key == "seen" && v.Current == "yes")
	}
	if !kept {
		t.Fatalf("keep decision did not persist the write: %+v", vars.Variables)
	}
	f.must("POST", base+"/persist", map[string]any{"keep": true}, asUI, 409, nil)
}

func TestAsyncRunAIChannelRules(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(1)
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "persist": "ask"}, asAI, 400, nil)
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "scopePolicy": "off"}, asAI, 400, nil)
	var start struct {
		RunUID string `json:"runUid"`
	}
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID}, asAI, 202, &start)
	f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
}

func TestAsyncRunBroadcastsOnTheGlobalEventStream(t *testing.T) {
	f := newCollFixture(t)
	collUID, envUID := f.runnerFixture(1)
	ch := make(chan string, 64)
	f.h.mu.Lock()
	f.h.clients[ch] = struct{}{}
	f.h.mu.Unlock()
	defer func() {
		f.h.mu.Lock()
		delete(f.h.clients, ch)
		f.h.mu.Unlock()
	}()
	f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": collUID, "envUid": envUID, "persist": "discard"}, asUI, 202, nil)
	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for !seen["done"] {
		select {
		case msg := <-ch:
			var e struct {
				Type  string `json:"type"`
				Event struct {
					Type string `json:"type"`
				} `json:"event"`
			}
			if json.Unmarshal([]byte(msg), &e) == nil && e.Type == "collrun" {
				seen[e.Event.Type] = true
			}
		case <-deadline:
			t.Fatalf("global stream saw %v", seen)
		}
	}
	if !seen["start"] || !seen["item"] {
		t.Fatalf("global stream saw %v", seen)
	}
}

// A single send persists the cookie jar only when it keeps session state, like
// variable writes; the default for an interactive send is keep.
func TestSendCookiesFollowPersistPolicy(t *testing.T) {
	f := newCollFixture(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "cookie-value-123456", Path: "/"})
	}))
	defer site.Close()
	u, _ := url.Parse(site.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Cookies"}, asUI, 201, &co)
	var req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "login", "method": "GET",
		"url": site.URL + "/login"}, asUI, 201, &req)
	envUID := f.newEnv("tok-value-123456")
	stored := func() int {
		cs, _ := f.st.ListCookies(co.UID + "|" + envUID + "|")
		return len(cs)
	}
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID, "envUid": envUID, "persist": "discard"}, asUI, 200, nil)
	if stored() != 0 {
		t.Fatal("a discarded send must not persist its cookies")
	}
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID, "envUid": envUID}, asUI, 200, nil)
	if stored() != 1 {
		t.Fatalf("an interactive send keeps session state by default; stored = %d", stored())
	}
}
