package collexec

import (
	"bytes"
	"compress/gzip"
	"context"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

func send1(e *env, it store.Item, layers ...varstore.Layer) *StepResult {
	return e.step(StepInput{Chain: chainOf(it), ScopePolicy: "off", Layers: layers})
}

func TestAuthBasicBearerAPIKeyAndInheritance(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	base := store.Item{Method: "GET", URL: js(rec.srv.URL)}

	it := base
	it.Auth = js(m{"type": "basic", "basic": m{"username": "ann", "password": "p w"}})
	send1(e, it)
	if req, _ := rec.last(); req.Header.Get("Authorization") != "Basic YW5uOnAgdw==" {
		t.Fatalf("basic: %v", req.Header)
	}

	it.Auth = js(m{"type": "apikey", "apikey": []m{{"key": "key", "value": "X-Api-Key"}, {"key": "value", "value": "k1"}}})
	// Postman array form: [{key:"key",value:<header name>},{key:"value",value:<secret>}].
	send1(e, it)
	if req, _ := rec.last(); req.Header.Get("X-Api-Key") != "k1" {
		t.Fatalf("apikey header: %v", req.Header)
	}

	it.Auth = js(m{"type": "apikey", "apikey": m{"key": "api_key", "value": "k2", "in": "query"}})
	send1(e, it)
	if req, _ := rec.last(); req.URL.Query().Get("api_key") != "k2" {
		t.Fatalf("apikey query: %s", req.URL)
	}

	// An explicit Authorization header wins over bearer auth.
	it.Auth = js(m{"type": "bearer", "bearer": m{"token": "from-auth"}})
	it.Headers = js([]m{{"key": "Authorization", "value": "Custom explicit"}})
	send1(e, it)
	if req, _ := rec.last(); req.Header.Get("Authorization") != "Custom explicit" {
		t.Fatalf("explicit header lost: %v", req.Header)
	}

	// Inherit: item inherits from folder, folder from collection; "none" on a folder cuts it off.
	ch := chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL), Auth: js(m{"type": "inherit"})})
	ch.Collection.Auth = js(m{"type": "bearer", "bearer": m{"token": "coll"}})
	ch.Folders = []store.Item{{UID: "f", Kind: "folder", Auth: js(m{"type": "inherit"})}}
	e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if req, _ := rec.last(); req.Header.Get("Authorization") != "Bearer coll" {
		t.Fatalf("inherit: %v", req.Header)
	}
	ch.Folders[0].Auth = js(m{"type": "noauth"})
	e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if req, _ := rec.last(); req.Header.Get("Authorization") != "" {
		t.Fatalf("noauth did not stop inheritance: %v", req.Header)
	}

	// Unsupported types warn instead of silently pretending.
	it = base
	it.Auth = js(m{"type": "awsv4"})
	res := send1(e, it)
	if res.Outcome != OutcomeSent || len(res.Warnings) == 0 {
		t.Fatalf("%+v", res)
	}
}

func TestAuthFieldsResolveVariablesAfterScripts(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	it := store.Item{Method: "GET", URL: js(rec.srv.URL), Auth: js(m{"type": "bearer", "bearer": m{"token": "{{tok}}"}})}
	send1(e, it, layer(varstore.ScopeCollection, map[string]string{"tok": "T-1"}))
	if req, _ := rec.last(); req.Header.Get("Authorization") != "Bearer T-1" {
		t.Fatalf("%v", req.Header)
	}
}

func TestBodyModes(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	v := layer(varstore.ScopeEnvironment, map[string]string{"n": "ann"})

	send1(e, store.Item{Method: "POST", URL: js(rec.srv.URL), Body: js(m{"mode": "raw", "raw": `{"name":"{{n}}"}`, "options": m{"raw": m{"language": "json"}}})}, v)
	req, body := rec.last()
	if body != `{"name":"ann"}` || req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("raw: %q %v", body, req.Header)
	}

	send1(e, store.Item{Method: "POST", URL: js(rec.srv.URL), Body: js(m{"mode": "urlencoded", "urlencoded": []m{{"key": "a b", "value": "x&y={{n}}"}, {"key": "skip", "value": "1", "disabled": true}}})}, v)
	req, body = rec.last()
	if body != "a+b=x%26y%3Dann" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("urlencoded: %q %v", body, req.Header)
	}

	send1(e, store.Item{Method: "POST", URL: js(rec.srv.URL), Body: js(m{"mode": "graphql", "graphql": m{"query": "{ u(n:\"{{n}}\") }", "variables": `{"a":1}`}})}, v)
	req, body = rec.last()
	if !strings.Contains(body, `"query":"{ u(n:\"ann\") }"`) || !strings.Contains(body, `"variables":{"a":1}`) || req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("graphql: %q", body)
	}

	res := send1(e, store.Item{Method: "POST", URL: js(rec.srv.URL), Body: js(m{"mode": "formdata", "formdata": []m{
		{"key": "who", "value": "{{n}}", "type": "text"}, {"key": "f", "type": "file", "src": "/etc/passwd"}}})}, v)
	req, body = rec.last()
	mt, params, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if mt != "multipart/form-data" {
		t.Fatalf("multipart ct %q", req.Header.Get("Content-Type"))
	}
	pr := multipart.NewReader(strings.NewReader(body), params["boundary"])
	p, err := pr.NextPart()
	if err != nil || p.FormName() != "who" {
		t.Fatalf("part: %v", err)
	}
	if _, err := pr.NextPart(); err == nil {
		t.Fatal("file part must be skipped (no local path reads)")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning for the skipped file part")
	}
}

func TestParamsAreAuthoritativeAndPayloadsReachTheWireAsTyped(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	send1(e, store.Item{Method: "GET", URL: js(rec.srv.URL + "/p?old=1"),
		Params: js([]m{{"key": "q", "value": "' OR 1=1--"}, {"key": "e", "value": "a%2Fb"}})})
	req, _ := rec.last()
	if strings.Contains(req.URL.RawQuery, "old=1") || req.URL.Query().Get("q") != "' OR 1=1--" || req.URL.RawQuery != "q='%20OR%201%3D1--&e=a%2Fb" {
		t.Fatalf("query %q", req.URL.RawQuery)
	}
	// Without params the URL query is kept verbatim.
	send1(e, store.Item{Method: "GET", URL: js(rec.srv.URL + "/p?raw=%27x")})
	if req, _ := rec.last(); req.URL.RawQuery != "raw=%27x" {
		t.Fatalf("verbatim query %q", req.URL.RawQuery)
	}
}

func TestURLWithoutSchemeDefaultsToHTTP(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	res := send1(e, store.Item{Method: "GET", URL: js(strings.TrimPrefix(rec.srv.URL, "http://") + "/noscheme")})
	if res.Outcome != OutcomeSent {
		t.Fatalf("%+v", res)
	}
	if req, _ := rec.last(); req.URL.Path != "/noscheme" {
		t.Fatal(req.URL)
	}
	if res := send1(e, store.Item{Method: "GET", URL: js("")}); res.Outcome != OutcomeError {
		t.Fatalf("empty url: %+v", res)
	}
}

const aesFormCodec = `
meta = {"id": "wrap-form", "title": "wrap", "apply_on_send": True}
def match(flow, side):
    return flow.host.startswith("127.")
def decode(flow, side, raw):
    return {"plaintext": raw}
def encode(flow, side, plaintext):
    return json_encode({"content": "ENC:" + plaintext})
`

func TestCodecEncodesFormSubmissionStyleBodies(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	c, err := msgcodec.Compile("wrap-form", aesFormCodec)
	if err != nil {
		t.Fatal(err)
	}
	c.Meta.Enabled = true
	e.pipe.Encoder = MsgcodecEncoder{Codecs: []*msgcodec.Codec{c}}
	it := store.Item{Method: "POST", URL: js(rec.srv.URL), Body: js(m{"mode": "raw", "raw": `{"name":"{{n}}"}`})}
	res := send1(e, it, layer(varstore.ScopeEnvironment, map[string]string{"n": "ann"}))
	_, body := rec.last()
	if body != `{"content":"ENC:{\"name\":\"ann\"}"}` || !contains(res.Applied, "codec:wrap-form") {
		t.Fatalf("auto codec: %q %v", body, res.Applied)
	}
	// The stored flow keeps the true wire bytes.
	f, _ := e.st.GetFlow(res.FlowID)
	if f.ReqLen != int64(len(body)) {
		t.Fatalf("stored request length %d vs wire %d", f.ReqLen, len(body))
	}
	// settings.codec = off skips it; explicit id applies it; unknown id errors.
	it.Settings = js(m{"codec": "off"})
	send1(e, it, layer(varstore.ScopeEnvironment, map[string]string{"n": "ann"}))
	if _, b := rec.last(); b != `{"name":"ann"}` {
		t.Fatalf("off: %q", b)
	}
	it.Settings = js(m{"codec": "wrap-form"})
	send1(e, it, layer(varstore.ScopeEnvironment, map[string]string{"n": "ann"}))
	if _, b := rec.last(); !strings.HasPrefix(b, `{"content":"ENC:`) {
		t.Fatalf("explicit: %q", b)
	}
	it.Settings = js(m{"codec": "missing"})
	if res := send1(e, it, layer(varstore.ScopeEnvironment, map[string]string{"n": "ann"})); res.Outcome != OutcomeError || !strings.Contains(res.Error, "codec") {
		t.Fatalf("missing codec: %+v", res)
	}
}

func TestDeclarativeAssertions(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Env", "prod")
		w.Header().Set("Content-Encoding", "gzip")
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write([]byte(`{"user":{"id":7,"name":"ann","tags":["a","b"]},"ok":true}`))
		zw.Close()
		w.Write(buf.Bytes())
	})
	as := []m{
		{"type": "status", "op": "eq", "value": 200},
		{"type": "status", "op": "in", "value": []int{200, 204}},
		{"type": "status", "op": "eq", "value": 500, "name": "wrong status"},
		{"type": "header", "header": "x-env", "op": "contains", "value": "pro"},
		{"type": "header", "header": "X-Missing", "op": "notexists"},
		{"type": "body", "op": "contains", "value": `"name":"ann"`},
		{"type": "jsonpath", "path": "$.user.id", "op": "eq", "value": 7},
		{"type": "jsonpath", "path": "$.user.tags[1]", "op": "eq", "value": "b"},
		{"type": "jsonpath", "path": "$.user.id", "op": "gt", "value": 5},
		{"type": "jsonpath", "path": "$.nope", "op": "exists"},
		{"type": "jsonpath", "path": "$..id", "op": "exists", "name": "recursive"},
		{"type": "time", "op": "lt", "value": 60000},
		{"type": "weird", "op": "eq"},
	}
	res := send1(e, store.Item{Method: "GET", URL: js(rec.srv.URL), Assertions: js(as)})
	want := []TestStatus{TestPass, TestPass, TestFail, TestPass, TestPass, TestPass, TestPass, TestPass, TestPass, TestFail, TestUnsupported, TestPass, TestUnsupported}
	if len(res.Tests) != len(want) {
		t.Fatalf("%d tests: %+v", len(res.Tests), res.Tests)
	}
	for i, w := range want {
		if res.Tests[i].Status != w {
			t.Errorf("assertion %d (%s): got %s want %s (%+v)", i, res.Tests[i].Name, res.Tests[i].Status, w, res.Tests[i])
		}
	}
	if res.Tests[0].Source != "assertion:1" || res.Tests[0].Owner != "request" {
		t.Fatalf("meta %+v", res.Tests[0])
	}
}

func TestResponseBodyIsDecompressedForScripts(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		zw.Write([]byte("hello-gz"))
		zw.Close()
	})
	var got string
	e.pipe.Exec = ExecutorFunc(func(_ context.Context, c ScriptCall) (ScriptOutcome, error) {
		got = string(c.Ctx.Response.Body)
		return ScriptOutcome{}, nil
	})
	e.pipe.Trust = allTrusted{}
	ch := chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL), Events: events("test", "x")})
	e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if got != "hello-gz" {
		t.Fatalf("body %q", got)
	}
}

func TestJarMatching(t *testing.T) {
	j := NewJar()
	now := time.Unix(1_000_000, 0)
	u, _ := url.Parse("https://api.example.com/v1/users")
	j.Store(u, []*http.Cookie{
		{Name: "host", Value: "1", Path: "/"},
		{Name: "dom", Value: "2", Domain: "example.com", Path: "/"},
		{Name: "sec", Value: "3", Path: "/", Secure: true},
		{Name: "deep", Value: "4", Path: "/v1/users/x"},
		{Name: "bad", Value: "5", Domain: "evil.com", Path: "/"},
		{Name: "exp", Value: "6", Path: "/", MaxAge: 10},
	}, now)
	got := j.Header(u, now)
	for _, w := range []string{"host=1", "dom=2", "sec=3", "exp=6"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in %q", w, got)
		}
	}
	if strings.Contains(got, "deep") || strings.Contains(got, "bad") {
		t.Errorf("wrong cookies attached: %q", got)
	}
	// Sibling host: only the domain cookie matches. Plain http drops Secure.
	o, _ := url.Parse("http://www.example.com/v1/users")
	if g := j.Header(o, now); g != "dom=2" {
		t.Errorf("sibling: %q", g)
	}
	// Expiry and deletion.
	if g := j.Header(u, now.Add(time.Minute)); strings.Contains(g, "exp=") {
		t.Errorf("expired cookie sent: %q", g)
	}
	j.Store(u, []*http.Cookie{{Name: "host", Path: "/", MaxAge: -1}}, now)
	if g := j.Header(u, now); strings.Contains(g, "host=1") {
		t.Errorf("deleted cookie sent: %q", g)
	}
	if len(j.List(now)) == 0 {
		t.Error("list empty")
	}
	j.Clear()
	if j.Header(u, now) != "" {
		t.Error("clear")
	}
}

func TestPinMatches(t *testing.T) {
	u, _ := url.Parse("https://api.example.com/x")
	for pin, want := range map[string]bool{
		"": true, "api.example.com": true, "API.example.com:443": true, "api.example.com:8443": false,
		"https://api.example.com": true, "http://api.example.com": false, "other.example.com": false,
	} {
		if got := pinMatches(pin, u); got != want {
			t.Errorf("pin %q: got %v want %v", pin, got, want)
		}
	}
}

func TestChainFromItemsAndLoad(t *testing.T) {
	coll := store.Collection{UID: "c", Name: "C"}
	items := []store.Item{
		{UID: "f1", Kind: "folder", Name: "outer"},
		{UID: "f2", Kind: "folder", Name: "inner", ParentUID: "f1"},
		{UID: "r", Kind: "request", Name: "req", ParentUID: "f2"},
	}
	ch, err := ChainFromItems(coll, items, "r")
	if err != nil || len(ch.Folders) != 2 || ch.Folders[0].UID != "f1" || ch.Folders[1].UID != "f2" {
		t.Fatalf("%+v %v", ch, err)
	}
	if _, err := ChainFromItems(coll, items, "f1"); err == nil {
		t.Fatal("folder accepted as request")
	}
	if _, err := ChainFromItems(coll, items, "zzz"); err == nil {
		t.Fatal("missing accepted")
	}
	// Cycles in parent links must not hang.
	cyc := []store.Item{{UID: "a", Kind: "folder", ParentUID: "b"}, {UID: "b", Kind: "folder", ParentUID: "a"}, {UID: "r", Kind: "request", ParentUID: "a"}}
	if _, err := ChainFromItems(coll, cyc, "r"); err != nil {
		t.Fatal(err)
	}
}

func TestStepMisuse(t *testing.T) {
	if _, err := (&Pipeline{}).Step(nil, StepInput{}); err == nil {
		t.Fatal("expected error")
	}
	e := newEnv(t)
	if _, err := e.pipe.Step(nil, StepInput{}); err == nil {
		t.Fatal("expected error for no item")
	}
	bare := &Pipeline{Sender: e.snd}
	if _, err := bare.Step(nil, StepInput{Chain: chainOf(store.Item{})}); err == nil {
		t.Fatal("expected not-initialised error")
	}
}

func TestSettingsMergeAndTimeout(t *testing.T) {
	e := newEnv(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(400 * time.Millisecond) }))
	defer slow.Close()
	ch := chainOf(store.Item{Method: "GET", URL: js(slow.URL), Settings: js(m{"timeoutMs": 50})})
	ch.Collection.Settings = js(m{"timeoutMs": 60000})
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeError || res.Response == nil || res.Response.Error == "" {
		t.Fatalf("item timeout should override collection: %+v", res)
	}
}
