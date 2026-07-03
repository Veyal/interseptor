package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/mcp"
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
	h := New(s, eng, nil, nil, nil)
	return h, s, eng
}

// A loopback request must not be able to relocate the process to an arbitrary
// path via /api/project/switch — only plain project names are accepted.
func TestSwitchProjectRejectsPaths(t *testing.T) {
	h, _, _ := newHub(t)
	h.SwitchProject = func(string) error { return nil } // stub; the real one re-execs
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(target string) int {
		body, _ := json.Marshal(map[string]string{"target": target})
		resp, err := http.Post(ts.URL+"/api/project/switch", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post %q: %v", target, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	for _, bad := range []string{"/tmp/evil", "../../etc", "~/x", `..\..\x`, "-rf", ".", ".."} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Fatalf("path-like target %q: expected 400, got %d", bad, code)
		}
	}
	if code := post("clientA"); code != http.StatusAccepted {
		t.Fatalf("plain name: expected 202, got %d", code)
	}
}

// The "path" field is a deliberate, separate opt-in for an operator-chosen
// save folder: absolute paths are accepted there (unlike "target"), but a
// drive/filesystem root is rejected, and the switch is remembered so it
// reappears in the project list without retyping the full path.
func TestSwitchProjectAcceptsExplicitPath(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	var gotTarget string
	h.SwitchProject = func(target string) error { gotTarget = target; return nil }
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	postPath := func(path string) (int, string) {
		body, _ := json.Marshal(map[string]string{"path": path})
		resp, err := http.Post(ts.URL+"/api/project/switch", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post %q: %v", path, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(resp.Body)
	}

	custom := filepath.Join(t.TempDir(), "acme-engagement")
	if code, _ := postPath(custom); code != http.StatusAccepted {
		t.Fatalf("absolute path: expected 202, got %d", code)
	}
	time.Sleep(350 * time.Millisecond) // the switch fires after a short delay
	if gotTarget != custom {
		t.Fatalf("SwitchProject called with %q, want %q", gotTarget, custom)
	}

	list := readExternalProjects(h.GlobalDir)
	if len(list) != 1 || list[0].Path != custom {
		t.Fatalf("expected the path to be remembered, got %+v", list)
	}

	// A filesystem/drive root must still be rejected even via "path".
	root := filepath.VolumeName(custom) + string(filepath.Separator)
	if root == string(filepath.Separator) {
		root = string(filepath.Separator)
	}
	if code, msg := postPath(root); code != http.StatusBadRequest {
		t.Fatalf("drive root %q: expected 400, got %d (%s)", root, code, msg)
	}
}

// Clearing the activity feed must delete the persisted rows, not just the client
// copy — otherwise it reappears on reload now that the feed is stored.
func TestClearActivityEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertActivity(&store.Activity{TS: 1, Tool: "list_flows", OK: true})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/activity", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got, _ := s.ListActivity(50); len(got) != 0 {
		t.Fatalf("after clear: got %d, want 0", len(got))
	}
}

// API-key auth is opt-in: the /mcp endpoint is open until a key exists, then it
// requires a valid bearer token. The /api surface stays loopback-trust regardless.
func TestMCPEndpointAuth(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(auth string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(""); code == http.StatusUnauthorized {
		t.Fatalf("keyless: /mcp should be open, got 401")
	}
	token, _, err := s.CreateAPIKey("agent")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if code := post(""); code != http.StatusUnauthorized {
		t.Fatalf("with a key, no token: expected 401, got %d", code)
	}
	if code := post("Bearer nope_" + token[4:]); code != http.StatusUnauthorized {
		t.Fatalf("bad token: expected 401, got %d", code)
	}
	if code := post("Bearer " + token); code == http.StatusUnauthorized {
		t.Fatalf("valid token: should pass the guard, got 401")
	}
	// /api stays open on loopback trust even with a key present.
	resp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("GET flows: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("/api should remain loopback-trust, got 401")
	}
}

// The UI's MCP descriptor (api.go) must list exactly the tools the MCP server
// registers — a guard against the "24 vs 36 tools" drift recurring.
func TestMCPDescriptorMatchesRegistry(t *testing.T) {
	registered := mcp.New("http://127.0.0.1:1").ToolNames()
	tools, _ := mcpDescriptor["tools"].([]map[string]string)
	listed := map[string]bool{}
	for _, tt := range tools {
		listed[tt["name"]] = true
	}
	if len(tools) != len(registered) {
		t.Fatalf("descriptor lists %d tools, registry registers %d — sync internal/control/api.go", len(tools), len(registered))
	}
	for _, name := range registered {
		if !listed[name] {
			t.Fatalf("tool %q is registered but missing from the UI descriptor (internal/control/api.go)", name)
		}
	}
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

func TestListFlowsSortAsc(t *testing.T) {
	h, s, _ := newHub(t)
	for i := 0; i < 5; i++ {
		s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows?sort=id&dir=asc&limit=2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			ID int64 `json:"id"`
		} `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Flows) != 2 || out.Flows[0].ID >= out.Flows[1].ID {
		t.Fatalf("asc page1 = %+v", out.Flows)
	}
	cur := out.Flows[len(out.Flows)-1]
	resp2, err := http.Get(ts.URL + "/api/flows?sort=id&dir=asc&limit=2&curId=" + itoa(cur.ID))
	if err != nil {
		t.Fatalf("GET page2: %v", err)
	}
	defer resp2.Body.Close()
	var out2 struct {
		Flows []struct {
			ID int64 `json:"id"`
		} `json:"flows"`
	}
	json.NewDecoder(resp2.Body).Decode(&out2)
	if len(out2.Flows) != 2 || out2.Flows[0].ID <= cur.ID {
		t.Fatalf("asc page2 = %+v after cur %d", out2.Flows, cur.ID)
	}
}

func TestActivityFeed(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(tool, summary string, ok bool) {
		body, _ := json.Marshal(map[string]any{"tool": tool, "summary": summary, "ok": ok, "result": "r", "ms": 12})
		resp, err := http.Post(ts.URL+"/api/activity", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("POST activity: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("POST activity status %d", resp.StatusCode)
		}
	}
	post("send_request", "method=POST url=/login", true)
	post("active_scan", "target=https://x", true)

	resp, err := http.Get(ts.URL + "/api/activity")
	if err != nil {
		t.Fatalf("GET activity: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Activity []store.Activity `json:"activity"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Activity) != 2 {
		t.Fatalf("expected 2 activity items, got %d", len(out.Activity))
	}
	if out.Activity[0].Tool != "active_scan" { // newest first
		t.Fatalf("expected newest-first, got %q", out.Activity[0].Tool)
	}
	if out.Activity[0].ID == 0 || out.Activity[0].TS == 0 {
		t.Fatalf("server should assign id+ts: %+v", out.Activity[0])
	}

	// A bad POST (no tool) is rejected.
	bad, err := http.Post(ts.URL+"/api/activity", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST bad activity: %v", err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty activity should be 400, got %d", bad.StatusCode)
	}
}

func TestProjectListAndSwitch(t *testing.T) {
	h, _, _ := newHub(t)
	gd := t.TempDir()
	os.MkdirAll(filepath.Join(gd, "projects", "acme"), 0o755)
	os.MkdirAll(filepath.Join(gd, "projects", "beta"), 0o755)
	h.GlobalDir = gd
	h.ProjectName = "default"
	done := make(chan string, 1)
	h.SwitchProject = func(target string) error { done <- target; return nil }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// GET /api/project: default first, then named projects sorted.
	resp, err := http.Get(ts.URL + "/api/project")
	if err != nil {
		t.Fatalf("GET project: %v", err)
	}
	var info struct {
		Current  string `json:"current"`
		Projects []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"projects"`
		CanSwitch bool `json:"canSwitch"`
	}
	json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if info.Current != "default" || !info.CanSwitch {
		t.Fatalf("project info: %+v", info)
	}
	if len(info.Projects) != 3 || info.Projects[0].Name != "default" || info.Projects[1].Name != "acme" || info.Projects[2].Name != "beta" {
		t.Fatalf("projects: %+v", info.Projects)
	}
	for _, p := range info.Projects {
		if p.Path != "" {
			t.Fatalf("named project %q should have an empty path, got %q", p.Name, p.Path)
		}
	}

	// Empty target → 400.
	bad, err := http.Post(ts.URL+"/api/project/switch", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("switch empty: %v", err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty switch should be 400, got %d", bad.StatusCode)
	}

	// Valid target → 202, and the switch callback fires with that target.
	ok, err := http.Post(ts.URL+"/api/project/switch", "application/json", strings.NewReader(`{"target":"acme"}`))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	ok.Body.Close()
	if ok.StatusCode != http.StatusAccepted {
		t.Fatalf("switch should be 202, got %d", ok.StatusCode)
	}
	select {
	case got := <-done:
		if got != "acme" {
			t.Fatalf("switched to %q, want acme", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SwitchProject callback was not invoked")
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

func TestFlowBodyDownload(t *testing.T) {
	h, s, _ := newHub(t)
	payload := `{"large":true}`
	hash, n := (&projectAPI{h}).storeBody([]byte(payload))
	id, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "x.com", Path: "/api",
		HTTPVersion: "HTTP/1.1", Status: 200, Mime: "application/json",
		ReqHeaders: map[string][]string{
			"Host":         {"x.com"},
			"Content-Type": {"application/json"},
		},
		ReqBodyHash: hash, ReqLen: n,
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/body?side=req")
	if err != nil {
		t.Fatalf("GET body: %v", err)
	}
	defer resp.Body.Close()
	if got := readAll(resp.Body); got != payload {
		t.Fatalf("body = %q, want %q", got, payload)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	disp := resp.Header.Get("Content-Disposition")
	if !strings.Contains(disp, "flow-"+itoa(id)+"-req.json") {
		t.Fatalf("content-disposition = %q", disp)
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

func TestRepeaterSendAndHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "pong")
	}))
	defer upstream.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"method":"GET","url":"` + upstream.URL + `/r","headers":"X-A: 1","body":""}`
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("repeater send: %v", err)
	}
	defer resp.Body.Close()
	var sent map[string]any
	json.NewDecoder(resp.Body).Decode(&sent)
	if sent["status"] != float64(200) || sent["path"] != "/r" {
		t.Fatalf("unexpected repeater flow: %v", sent)
	}

	// Shows in repeater history, but NOT in the proxy history (excluded).
	hr, err := http.Get(ts.URL + "/api/repeater/history")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	defer hr.Body.Close()
	var hist struct {
		Flows []map[string]any `json:"flows"`
	}
	json.NewDecoder(hr.Body).Decode(&hist)
	if len(hist.Flows) != 1 {
		t.Fatalf("expected 1 repeater flow, got %d", len(hist.Flows))
	}

	pr, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("flows: %v", err)
	}
	defer pr.Body.Close()
	var prox struct {
		Flows []map[string]any `json:"flows"`
	}
	json.NewDecoder(pr.Body).Decode(&prox)
	if len(prox.Flows) != 0 {
		t.Fatalf("repeater flow should be excluded from proxy history, got %d", len(prox.Flows))
	}
}

func TestScannerRunFindsIssues(t *testing.T) {
	h, s, _ := newHub(t)
	// An HTTPS flow with no HSTS and wildcard CORS → two findings.
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string{"Content-Type": {"text/html"}, "Access-Control-Allow-Origin": {"*"}},
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
	if err != nil {
		t.Fatalf("scanner run: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Issues []map[string]any `json:"issues"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Issues) < 2 {
		t.Fatalf("expected at least 2 issues, got %d", len(out.Issues))
	}
	var foundHeaders, foundCORS bool
	for _, i := range out.Issues {
		// Security headers are now a single consolidated finding (was a separate HSTS title).
		if i["title"] == "Missing security response headers" {
			foundHeaders = true
		}
		if i["title"] == "Overly permissive CORS policy" {
			foundCORS = true
		}
	}
	if !foundHeaders || !foundCORS {
		t.Fatalf("missing expected findings: headers=%v cors=%v (%v)", foundHeaders, foundCORS, out.Issues)
	}
}

func TestScopeFiltersHistoryAndScanner(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.acme.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "cdn.other.com", Path: "/", Status: 200})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// No scope yet → everything in scope.
	if n := flowCount(t, ts.URL+"/api/flows?inScope=1"); n != 2 {
		t.Fatalf("no scope: expected 2 in-scope, got %d", n)
	}

	// Add an include rule for *.acme.com.
	resp, err := http.Post(ts.URL+"/api/scope", "application/json",
		strings.NewReader(`{"action":"include","host":"*.acme.com","enabled":true}`))
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	resp.Body.Close()

	if n := flowCount(t, ts.URL+"/api/flows?inScope=1"); n != 1 {
		t.Fatalf("with scope: expected 1 in-scope (acme), got %d", n)
	}
	if n := flowCount(t, ts.URL+"/api/flows"); n != 2 {
		t.Fatalf("unfiltered history should still show all 2, got %d", n)
	}
}

func flowCount(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []map[string]any `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return len(out.Flows)
}

func TestProjectExportImportRoundTrip(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.test", Path: "/", Status: 200})
	s.CreateRule(&store.Rule{Enabled: true, Type: "req-header", Match: "User-Agent: .*", Replace: "User-Agent: x"})
	s.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.test"})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Export the project.
	er, err := http.Get(ts.URL + "/api/export/project")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	bundle, _ := io.ReadAll(er.Body)
	er.Body.Close()

	// Fresh hub; import the bundle.
	h2, s2, _ := newHub(t)
	ts2 := httptest.NewServer(h2.Handler())
	defer ts2.Close()
	ir, err := http.Post(ts2.URL+"/api/import/project", "application/json", bytes.NewReader(bundle))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var res struct {
		ImportedFlows, ImportedRules, ImportedScope int
	}
	json.NewDecoder(ir.Body).Decode(&res)
	ir.Body.Close()
	if res.ImportedFlows != 1 || res.ImportedRules != 1 || res.ImportedScope != 1 {
		t.Fatalf("import counts wrong: %+v", res)
	}
	if rules, _ := s2.ListRules(); len(rules) != 1 {
		t.Fatalf("rules not restored: %d", len(rules))
	}
	if sc, _ := s2.ListScopeRules(); len(sc) != 1 {
		t.Fatalf("scope not restored: %d", len(sc))
	}
	_ = s
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
