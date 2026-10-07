package collrun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

type allowScope struct{ hosts map[string]bool }

func (a allowScope) HostInScope(h string) bool { return a.hosts[strings.ToLower(h)] }
func (a allowScope) HasIncludes() bool         { return true }

type e2e struct {
	t     *testing.T
	st    *store.Store
	srv   *httptest.Server
	coll  *store.Collection
	env   *store.Environment
	hits  atomic.Int32
	users []string
}

func js(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func events(listen string, lines ...string) json.RawMessage {
	return js([]any{map[string]any{"listen": listen, "script": map[string]any{"exec": lines}}})
}

// newE2E builds a login -> me collection against a local server that issues a
// bearer token and rejects anything else.
func newE2E(t *testing.T) *e2e {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := &e2e{t: t, st: st}
	x.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.hits.Add(1)
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"token":"canary-token-AAAA1111BBBB2222"}`)
		case "/me":
			if r.Header.Get("Authorization") != "Bearer canary-token-AAAA1111BBBB2222" {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"alice"}`)
		case "/user":
			x.users = append(x.users, r.URL.Query().Get("u"))
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(x.srv.Close)

	x.coll, err = st.CreateCollection(store.Collection{Name: "E2E API", Caps: js([]string{"vars.read", "vars.write"})})
	if err != nil {
		t.Fatal(err)
	}
	x.env, err = st.CreateEnvironment(store.Environment{Name: "local", Kind: "env"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetVariables(store.VarOwnerEnvironment, x.env.UID, []store.Variable{
		{OwnerKind: store.VarOwnerEnvironment, OwnerUID: x.env.UID, Key: "baseUrl", Type: store.VarTypeDefault, InitialValue: x.srv.URL, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *e2e) add(name, method, path string, mod func(*store.Item)) *store.Item {
	it := store.Item{CollectionUID: x.coll.UID, Kind: "request", Name: name, Method: method, URL: js("{{baseUrl}}" + path)}
	if mod != nil {
		mod(&it)
	}
	out, err := x.st.CreateItem(it)
	if err != nil {
		x.t.Fatal(err)
	}
	return out
}

func (x *e2e) backend(pins ...string) *StoreBackend {
	snd := sender.New(x.st, capture.New(x.st))
	host := strings.TrimPrefix(x.srv.URL, "http://")
	host = host[:strings.LastIndex(host, ":")]
	return NewStoreBackend(StoreConfig{Store: x.st, Sender: snd, Scope: allowScope{map[string]bool{host: true}}, PinnedHashes: pins})
}

func (x *e2e) allHashes() []string {
	coll, items, _ := x.backend().Load(x.coll.UID) // reload: caps are bound into hashes
	var out []string
	for _, s := range CollectionScripts(coll, items) {
		out = append(out, s.Hash)
	}
	return out
}

func (x *e2e) tokenChain() {
	x.add("login", "POST", "/login", func(it *store.Item) {
		it.Events = events("test",
			"const j = pm.response.json();",
			"pm.environment.set('token', j.token);",
			"pm.test('login ok', () => pm.response.to.have.status(200));")
	})
	x.add("me", "GET", "/me", func(it *store.Item) {
		it.Headers = js([]map[string]any{{"key": "Authorization", "value": "Bearer {{token}}"}})
		it.Events = events("test", "pm.test('is alice', () => pm.expect(pm.response.json().name).to.eql('alice'));")
	})
}

func (x *e2e) run(b Backend, o Options) *Report {
	x.t.Helper()
	o.CollectionUID, o.EnvUID = x.coll.UID, x.env.UID
	rep, err := New(b, x.st).Run(context.Background(), o)
	if err != nil {
		x.t.Fatal(err)
	}
	return rep
}

func TestE2EScriptsFlowAndExitCodes(t *testing.T) {
	x := newE2E(t)
	x.tokenChain()

	// Unapproved scripts: headless refuses before sending anything.
	rep := x.run(x.backend(), Options{FailOnQuarantine: true})
	if rep.ExitCode() != ExitNotApproved || x.hits.Load() != 0 {
		t.Fatalf("exit %d hits %d status %s", rep.ExitCode(), x.hits.Load(), rep.Status)
	}

	// Pinned hashes approve them for this process; the token chains with discard.
	rep = x.run(x.backend(x.allHashes()...), Options{FailOnQuarantine: true, Persist: PersistDiscard})
	if rep.ExitCode() != ExitPass || rep.Totals.Sent != 2 || rep.Totals.Pass != 2 {
		b, _ := json.MarshalIndent(rep, "", " ")
		t.Fatalf("exit %d\n%s", rep.ExitCode(), b)
	}
	if rep.Persist.Committed != 0 || len(rep.Persist.Pending) != 1 || rep.Persist.Pending[0].Key != "token" {
		t.Fatalf("persist %+v", rep.Persist)
	}
	cur, _ := x.st.ListCurrentValues(store.VarOwnerEnvironment, x.env.UID)
	if len(cur) != 0 {
		t.Fatalf("discard wrote %d current values to the store", len(cur))
	}
	for _, it := range rep.Items {
		if it.FlowID == 0 {
			t.Fatalf("item %s has no flow id (History link)", it.Name)
		}
	}

	// Without scripts {{token}} is never set: the send is blocked, not sent
	// with a literal placeholder, and the run exits with a runtime error.
	rep = x.run(x.backend(), Options{NoScripts: true})
	if rep.Totals.Sent != 1 || rep.Totals.Unresolved != 1 || rep.ExitCode() != ExitRuntime {
		t.Fatalf("totals %+v", rep.Totals)
	}
}

func TestE2EAssertionFailureExitsOne(t *testing.T) {
	x := newE2E(t)
	x.add("me", "GET", "/me", func(it *store.Item) {
		it.Events = events("test", "pm.test('expects 200', () => pm.response.to.have.status(200));")
	})
	rep := x.run(x.backend(x.allHashes()...), Options{FailOnQuarantine: true})
	if rep.ExitCode() != ExitTestFail || rep.Totals.Fail != 1 {
		t.Fatalf("exit %d totals %+v", rep.ExitCode(), rep.Totals)
	}
	if !rep.Items[0].Problem() {
		t.Fatal("failed item must be a problem for rerun-failed")
	}
}

func TestE2EKeepPersistsAndDataDrivesIterations(t *testing.T) {
	x := newE2E(t)
	x.tokenChain()
	rep := x.run(x.backend(x.allHashes()...), Options{FailOnQuarantine: true, Persist: PersistKeep})
	if rep.ExitCode() != ExitPass || rep.Persist.Committed != 1 {
		t.Fatalf("exit %d persist %+v", rep.ExitCode(), rep.Persist)
	}
	cur, _ := x.st.ListCurrentValues(store.VarOwnerEnvironment, x.env.UID)
	if len(cur) != 1 || cur[0].Key != "token" || cur[0].Value != "canary-token-AAAA1111BBBB2222" {
		t.Fatalf("keep must store the current value: %+v", cur)
	}
	// Initial values are never touched.
	vs, _ := x.st.ListVariables(store.VarOwnerEnvironment, x.env.UID)
	for _, v := range vs {
		if v.Key == "token" && v.InitialValue != "" {
			t.Fatalf("initial value was overwritten: %q", v.InitialValue)
		}
	}

	y := newE2E(t)
	y.add("user", "GET", "/user?u={{user}}", nil)
	ds, err := ParseCSV(strings.NewReader("user\nalice@example.com\nbob@example.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	rep = y.run(y.backend(), Options{Data: ds})
	if rep.Iterations != 2 || len(y.users) != 2 || y.users[0] != "alice@example.com" || y.users[1] != "bob@example.com" {
		t.Fatalf("data rows not sent: %v", y.users)
	}
}

func TestE2EScopeBlockedAndUnresolved(t *testing.T) {
	x := newE2E(t)
	x.add("out of scope", "GET", "/x", func(it *store.Item) { it.URL = js("http://evil.example.net/x") })
	x.add("unresolved", "GET", "/y", func(it *store.Item) { it.URL = js("{{baseUrl}}/{{nope}}") })
	rep := x.run(x.backend(), Options{})
	if rep.Totals.ScopeBlocks != 1 || rep.Totals.Unresolved != 1 || rep.ExitCode() != ExitScope {
		b, _ := json.MarshalIndent(rep.Totals, "", " ")
		t.Fatalf("exit %d %s", rep.ExitCode(), b)
	}
	if x.hits.Load() != 0 {
		t.Fatalf("blocked requests must not be sent: %d hits", x.hits.Load())
	}
}

func TestE2ERunRowsPersistedMaskedAndRerunFailed(t *testing.T) {
	x := newE2E(t)
	x.tokenChain()
	x.add("fails", "GET", "/me", func(it *store.Item) {
		it.Events = events("test", "pm.test('forced', () => pm.response.to.have.status(200));")
	})
	b := x.backend(x.allHashes()...)
	rep := x.run(b, Options{FailOnQuarantine: true})
	runs, _ := x.st.ListRuns(x.coll.UID, 10)
	if len(runs) != 1 || runs[0].Status != StatusDone || runs[0].FinishedTS == 0 || runs[0].UID != rep.RunUID {
		t.Fatalf("runs: %+v", runs)
	}
	var sum RunSummary
	if err := json.Unmarshal([]byte(runs[0].SummaryJSON), &sum); err != nil || sum.Totals.Requests != 3 || sum.ExitCode != ExitTestFail {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	rows, _ := x.st.ListRunResults(rep.RunUID)
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	failed, err := FailedItemUIDs(x.st, rep.RunUID)
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed %v err %v", failed, err)
	}
	rep2 := x.run(b, Options{FailOnQuarantine: true, FailedFromRun: rep.RunUID})
	if len(rep2.Items) != 1 || rep2.Items[0].Name != "fails" {
		t.Fatalf("rerun failed ran %s", names(rep2))
	}
}

const canary = "CANARY-sekret-Q7x9ZZ41-do-not-leak"

func TestE2ESecretsNeverLeakIntoReportOrRows(t *testing.T) {
	x := newE2E(t)
	if _, err := x.st.UpdateCollection(store.Collection{UID: x.coll.UID, Name: x.coll.Name, ScopePolicy: x.coll.ScopePolicy,
		Caps: js([]string{"vars.read", "vars.write", "secrets.read"})}); err != nil {
		t.Fatal(err)
	}
	vars := []store.Variable{
		{OwnerKind: store.VarOwnerEnvironment, OwnerUID: x.env.UID, Key: "baseUrl", Type: store.VarTypeDefault, InitialValue: x.srv.URL, Enabled: true},
		{OwnerKind: store.VarOwnerEnvironment, OwnerUID: x.env.UID, Key: "apiSecret", Type: store.VarTypeSecret, Enabled: true},
	}
	if err := x.st.SetVariables(store.VarOwnerEnvironment, x.env.UID, vars); err != nil {
		t.Fatal(err)
	}
	if err := x.st.SetCurrentValue(store.VarOwnerEnvironment, x.env.UID, "apiSecret", canary, "test"); err != nil {
		t.Fatal(err)
	}
	x.add("leaky", "GET", "/user?u={{apiSecret}}", func(it *store.Item) {
		it.Headers = js([]map[string]any{{"key": "X-Api-Key", "value": "{{apiSecret}}"}})
		it.Events = events("test",
			"const s = pm.environment.get('apiSecret');",
			"console.log('secret is ' + s);",
			"pm.environment.set('copy', s);",
			"pm.test('has ' + s, () => pm.expect(s).to.eql('nope ' + s));")
	})
	b := x.backend()
	b2 := NewStoreBackend(StoreConfig{Store: x.st, Sender: sender.New(x.st, capture.New(x.st)), Scope: b.cfg.Scope,
		PinnedHashes: x.allHashes()})
	rep := x.run(b2, Options{FailOnQuarantine: true})
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("canary leaked into the report JSON:\n%s", raw)
	}
	rows, _ := x.st.ListRunResults(rep.RunUID)
	if len(rows) == 0 {
		t.Fatal("no rows persisted")
	}
	for _, r := range rows {
		if strings.Contains(r.ResultJSON, canary) {
			t.Fatalf("canary leaked into ix_run_results: %s", r.ResultJSON)
		}
	}
	runs, _ := x.st.ListRuns(x.coll.UID, 5)
	if strings.Contains(runs[0].SummaryJSON, canary) {
		t.Fatal("canary leaked into the run summary")
	}
	it := rep.Items[0]
	if it.URL == "" || strings.Contains(it.URL, canary) {
		t.Fatalf("url must be present and masked: %q", it.URL)
	}
	if len(it.Console) == 0 {
		t.Fatal("console output expected (masked), got none")
	}
}
