package control

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

const (
	e2eHMACSecret = "CANARY-HMAC-s3cr3t-9f31c7a2"
	e2eToken      = "CANARY-TOKEN-AAAA1111BBBB2222"
)

// e2eAPI is an HTTP API that needs a login token and an HMAC request signature.
type e2eAPI struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string // "METHOD path ok|denied"
}

func newE2EAPI(t *testing.T) *e2eAPI {
	a := &e2eAPI{}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		defer func() {
			a.mu.Lock()
			a.seen = append(a.seen, r.Method+" "+r.URL.Path+" "+status)
			a.mu.Unlock()
		}()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			fmt.Fprintf(w, `{"token":%q}`, e2eToken)
		case "/data":
			mac := hmac.New(sha256.New, []byte(e2eHMACSecret))
			mac.Write([]byte(r.Header.Get("X-Timestamp") + "GET" + r.URL.Path))
			want := hex.EncodeToString(mac.Sum(nil))
			if r.Header.Get("Authorization") != "Bearer "+e2eToken || r.Header.Get("X-Signature") != want {
				status = "denied"
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"error":"bad token or signature"}`)
				return
			}
			io.WriteString(w, `{"user":"ann","items":[1,2,3]}`)
		default:
			status = "denied"
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *e2eAPI) log() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

func e2ePostmanCollection(baseURL string) string {
	pre, _ := json.Marshal([]string{
		"const ts = String(Date.now());",
		"const sig = CryptoJS.HmacSHA256(ts + 'GET' + '/data', pm.environment.get('hmacKey')).toString(CryptoJS.enc.Hex);",
		"pm.request.headers.upsert({ key: 'X-Timestamp', value: ts });",
		"pm.request.headers.upsert({ key: 'X-Signature', value: sig });",
	})
	loginTest, _ := json.Marshal([]string{
		"pm.test('login ok', function () { pm.response.to.have.status(200); });",
		"pm.environment.set('token', pm.response.json().token);",
	})
	dataTest, _ := json.Marshal([]string{
		"pm.test('signed request accepted', function () { pm.response.to.have.status(200); });",
		"pm.test('body', function () { pm.expect(pm.response.json().user).to.equal('ann'); });",
	})
	return `{"info":{"name":"Signed API","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
 "variable":[{"key":"baseUrl","value":` + jsonString(baseURL) + `}],
 "auth":{"type":"bearer","bearer":[{"key":"token","value":"{{token}}"}]},
 "item":[
  {"name":"Login","event":[{"listen":"test","script":{"exec":` + string(loginTest) + `}}],
   "request":{"method":"POST","auth":{"type":"noauth"},"header":[{"key":"Content-Type","value":"application/json"}],
     "body":{"mode":"raw","raw":"{\"user\":\"ann\"}"},"url":{"raw":"{{baseUrl}}/login"}}},
  {"name":"Signed data","event":[{"listen":"prerequest","script":{"exec":` + string(pre) + `}},{"listen":"test","script":{"exec":` + string(dataTest) + `}}],
   "request":{"method":"GET","url":{"raw":"{{baseUrl}}/data"}}}]}`
}

func e2ePostmanEnvironment() string {
	return `{"name":"e2e env","_postman_variable_scope":"environment","values":[` +
		`{"key":"hmacKey","value":` + jsonString(e2eHMACSecret) + `,"type":"secret","enabled":true}]}`
}

// The whole story in one test, through the in-process control mux: import a
// Postman collection whose requests need a login token chain and an HMAC
// signature computed by a pre-request script, observe that the scripts are
// quarantined until trusted, trust them as the owner, run through the runner,
// and assert results, captured flows and that no secret leaks anywhere.
func TestEndToEndPostmanHMACTokenChainThroughTheRunner(t *testing.T) {
	f := newCollFixture(t)
	api := newE2EAPI(t)
	u, _ := url.Parse(api.srv.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})

	// 1. Import the collection and its environment.
	var imp struct {
		CollectionUID string `json:"collectionUid"`
		Scripts       bool   `json:"scriptsQuarantined"`
	}
	f.must("POST", "/api/import/collection/commit", e2ePostmanCollection(api.srv.URL), asUI, 201, &imp)
	f.must("POST", "/api/import/collection/commit", e2ePostmanEnvironment(), asUI, 201, nil)
	if imp.CollectionUID == "" || !imp.Scripts {
		t.Fatalf("import = %+v", imp)
	}
	var envs struct {
		Environments []struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"environments"`
	}
	f.must("GET", "/api/environments", nil, asUI, 200, &envs)
	envUID := ""
	for _, e := range envs.Environments {
		if e.Name == "e2e env" && e.Kind == "env" {
			envUID = e.UID
		}
	}
	if envUID == "" {
		t.Fatalf("environment not imported: %+v", envs)
	}

	// 2. Scripts arrive quarantined: nothing is trusted and nothing signs.
	var sheet struct {
		Scripts []struct {
			Trusted bool `json:"trusted"`
		} `json:"scripts"`
	}
	f.must("GET", "/api/collections/"+imp.CollectionUID+"/scripts", nil, asUI, 200, &sheet)
	if len(sheet.Scripts) != 3 {
		t.Fatalf("scripts in review sheet = %d, want 3", len(sheet.Scripts))
	}
	for _, s := range sheet.Scripts {
		if s.Trusted {
			t.Fatal("an imported script arrived trusted")
		}
	}
	var quarantined collrun.Progress
	runAndWait := func(persist string) (collrun.Progress, string, []string) {
		var start struct {
			RunUID string `json:"runUid"`
		}
		f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": imp.CollectionUID, "envUid": envUID, "persist": persist}, asUI, 202, &start)
		events := f.captureRunEvents(start.RunUID)
		p := f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
		return p, start.RunUID, events()
	}
	quarantined, _, _ = runAndWait("discard")
	if quarantined.Totals.Quarantined == 0 || quarantined.Totals.Pass != 0 {
		t.Fatalf("an untrusted collection must run without its scripts: %+v", quarantined.Totals)
	}
	// The token chain is script-driven, so {{token}} stays unresolved and the
	// signed request is blocked before it is ever sent (never a garbage header).
	if seen := api.log(); len(seen) != 1 || seen[0] != "POST /login ok" || quarantined.Totals.Unresolved != 1 {
		t.Fatalf("without scripts only the login may be sent: log=%v totals=%+v", seen, quarantined.Totals)
	}

	// 3. The owner trusts the scripts (UI session only) and grants capabilities.
	f.must("POST", "/api/collections/"+imp.CollectionUID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead, CapVarsWrite, CapSecretsRead}}, asAI, 403, nil)
	f.must("POST", "/api/collections/"+imp.CollectionUID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead, CapVarsWrite, CapSecretsRead}}, asUI, 200, nil)

	// 4. Run it through the runner, keeping variable writes.
	p, runUID, events := runAndWait("keep")
	if p.Status != collrun.StatusDone {
		t.Fatalf("run status %s", p.Status)
	}
	if p.Totals.Requests != 2 || p.Totals.Sent != 2 || p.Totals.Pass != 3 || p.Totals.Fail != 0 || p.Totals.Errors != 0 || p.Totals.Unsupported != 0 {
		t.Fatalf("totals = %+v", p.Totals)
	}
	if seen := api.log(); len(seen) < 3 || seen[len(seen)-2] != "POST /login ok" || seen[len(seen)-1] != "GET /data ok" {
		t.Fatalf("the HMAC-signed, token-authorised call must be accepted by the API: %v", seen)
	}

	// 5. Flows: every request is a collection flow in History with its context.
	var hist struct {
		Flows []struct {
			ID    int64  `json:"id"`
			Path  string `json:"path"`
			Flags int64  `json:"flags"`
		} `json:"flows"`
	}
	f.must("GET", "/api/flows?collection=only", nil, asUI, 200, &hist)
	paths := map[string]bool{}
	for _, fl := range hist.Flows {
		paths[fl.Path] = true
		if fl.Flags&store.FlagCollection == 0 {
			t.Fatalf("flow %d is not flagged as a collection flow", fl.ID)
		}
	}
	if !paths["/login"] || !paths["/data"] {
		t.Fatalf("collection flows in History = %v", paths)
	}
	var dataFlow int64
	for _, it := range p.Items {
		if it.Name == "Signed data" {
			dataFlow = it.FlowID
		}
	}
	fl, err := f.st.GetFlow(dataFlow)
	if err != nil || fl.Status != 200 {
		t.Fatalf("data flow: %v %+v", err, fl)
	}
	if got := fl.ReqHeaders["Authorization"]; len(got) != 1 || got[0] != "Bearer "+e2eToken {
		t.Fatalf("the wire flow keeps the true Authorization header (evidence): %v", got)
	}
	if ctx, ok, _ := f.st.GetFlowCtx(dataFlow); !ok || ctx.RunID != runUID || ctx.EnvUID != envUID {
		t.Fatalf("flow context = %+v ok=%v", ctx, ok)
	}

	// 6. Masking: neither the HMAC secret nor the token appears in any result,
	// event, report, listing or archive.
	var surfaces = map[string]string{}
	surfaces["progress"] = e2eJSON(p)
	surfaces["sse events"] = strings.Join(events, "\n")
	surfaces["persisted run rows"] = f.must("GET", "/api/runs/"+runUID, nil, asUI, 200, nil)
	surfaces["environments"] = f.must("GET", "/api/environments?reveal=1", nil, asAI, 200, nil)
	surfaces["environment variables (AI)"] = f.must("GET", "/api/variables/environment/"+envUID, nil, asAI, 200, nil)
	surfaces["collection (AI)"] = f.must("GET", "/api/collections/"+imp.CollectionUID, nil, asAI, 200, nil)
	surfaces["sync run (AI)"] = f.must("POST", "/api/collections/run", map[string]any{"collectionUid": imp.CollectionUID, "envUid": envUID}, asAI, 200, nil)
	surfaces["scripts sheet (AI)"] = f.must("GET", "/api/collections/"+imp.CollectionUID+"/scripts", nil, asAI, 200, nil)
	for name, body := range surfaces {
		for _, canary := range []string{e2eHMACSecret, e2eToken} {
			if strings.Contains(body, canary) {
				t.Errorf("%s leaks %q", name, canary)
			}
		}
	}
	// The secret variable never left the local store: a scrubbed archive drops
	// it, the token chain value is blanked from the exported collection data.
	archive := f.rawGet("/api/export/full")
	db := dbFromArchive(t, archive)
	if strings.Contains(string(db), e2eHMACSecret) {
		t.Error("the HMAC secret reached a full-project archive")
	}
	bundle := f.rawGet("/api/export/project")
	if strings.Contains(string(bundle), e2eHMACSecret) {
		t.Error("the HMAC secret reached the project bundle")
	}
}

func e2eJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (f *collFixture) rawGet(path string) []byte {
	f.t.Helper()
	resp, err := http.Get(f.ts.URL + path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		f.t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	return b
}

// captureRunEvents follows a run's SSE stream in the background; the returned
// function waits for the stream to end and returns every data line.
func (f *collFixture) captureRunEvents(runUID string) func() []string {
	f.t.Helper()
	resp, err := http.Get(f.ts.URL + "/api/runner/runs/" + runUID + "/events")
	if err != nil {
		f.t.Fatal(err)
	}
	var lines []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			if t := sc.Text(); strings.HasPrefix(t, "data: ") {
				lines = append(lines, strings.TrimPrefix(t, "data: "))
			}
		}
	}()
	return func() []string {
		<-done
		return lines
	}
}
