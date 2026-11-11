package curl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

type gotReq struct {
	Method, URI, Body string
	Header            http.Header
}

type execEnv struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	got    []gotReq
	dir    string
	marker string
}

func newExecEnv(t *testing.T) *execEnv {
	t.Helper()
	for _, bin := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	e := &execEnv{t: t, dir: t.TempDir()}
	e.marker = filepath.Join(e.dir, "pwned")
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.got = append(e.got, gotReq{r.Method, r.RequestURI, string(b), r.Header.Clone()})
		e.mu.Unlock()
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// run exports one request, executes the script with bash and returns the
// requests the server saw.
func (e *execEnv) run(it store.Item) ([]gotReq, *Output) {
	e.t.Helper()
	it.UID, it.CollectionUID, it.Kind = "i1", "c1", "request"
	if it.Name == "" {
		it.Name = "r"
	}
	b := store.CollectionsBundle{Collections: []store.Collection{{UID: "c1", Name: "c"}}, Items: []store.Item{it}}
	out, err := ExportCollection(b, "c1", Options{Shebang: true})
	if err != nil {
		e.t.Fatal(err)
	}
	e.mu.Lock()
	e.got = nil
	e.mu.Unlock()
	cmd := exec.Command("bash", "-c", string(out.Data))
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), "HOME="+e.dir, "CURL_HOME="+e.dir)
	res, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Errorf("script failed: %v\n%s\n--- script ---\n%s", err, res, out.Data)
	}
	if _, err := os.Stat(e.marker); err == nil {
		e.t.Fatalf("the script ran an injected command\n%s", out.Data)
	}
	if ents, _ := os.ReadDir(e.dir); len(ents) != 0 {
		e.t.Errorf("the script created %d file(s) in its working directory\n%s", len(ents), out.Data)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]gotReq(nil), e.got...), out
}

func jsr(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type mm = map[string]any

// hostile strings: every shell metacharacter, with a command that would leave a
// marker file if any of them were interpreted.
func (e *execEnv) hostile() []string {
	touch := "touch " + e.marker
	return []string{
		`it's a "quoted" value`,
		"$(" + touch + ")",
		"`" + touch + "`",
		"'; " + touch + "; echo '",
		"a\nb\tc",
		`back\slash \n $HOME ${HOME} !! ~ * ? | & ; < > ( ) # %`,
		"' -o " + e.marker + " '",
		"ünï©ödé ✓",
	}
}

func TestExecHeaderValuesArriveUnchanged(t *testing.T) {
	e := newExecEnv(t)
	for _, v := range e.hostile() {
		if strings.ContainsAny(v, "\n") {
			continue // a header value cannot hold a line break
		}
		got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + "/h"), Headers: jsr([]mm{{"key": "X-Test", "value": v}})})
		if len(got) != 1 || got[0].Header.Get("X-Test") != strings.TrimSpace(v) {
			t.Errorf("header %q arrived as %+v", v, got)
		}
	}
}

func TestExecBodiesArriveUnchanged(t *testing.T) {
	e := newExecEnv(t)
	for _, v := range append(e.hostile(), "@/etc/hosts", "<"+e.marker) {
		got, _ := e.run(store.Item{Method: "POST", URL: jsr(e.srv.URL + "/b"), Body: jsr(mm{"mode": "raw", "raw": v})})
		if len(got) != 1 || got[0].Body != v || got[0].Method != "POST" {
			t.Errorf("raw body %q arrived as %+v", v, got)
		}
		got, _ = e.run(store.Item{Method: "POST", URL: jsr(e.srv.URL + "/b"), Body: jsr(mm{"mode": "graphql", "graphql": mm{"query": v}})})
		var p struct{ Query string }
		if len(got) != 1 || json.Unmarshal([]byte(got[0].Body), &p) != nil || p.Query != v {
			t.Errorf("graphql query %q arrived as %+v", v, got)
		}
	}
}

func TestExecFormFieldsArriveUnchanged(t *testing.T) {
	e := newExecEnv(t)
	vals := append(e.hostile(), "@/etc/hosts", "</etc/hosts", "a=b&c=d", "x;type=text/html", "100%", "a+b c")
	for _, v := range vals {
		got, _ := e.run(store.Item{Method: "POST", URL: jsr(e.srv.URL + "/f"), Body: jsr(mm{"mode": "urlencoded", "urlencoded": []mm{{"key": "k", "value": v}, {"key": "z", "value": "1"}}})})
		if len(got) != 1 {
			t.Errorf("urlencoded %q: %d requests", v, len(got))
			continue
		}
		form := parseForm(got[0].Body)
		if len(form) != 2 || form[0] != [2]string{"k", v} || form[1] != [2]string{"z", "1"} {
			t.Errorf("urlencoded value %q arrived as %q (%v)", v, got[0].Body, form)
		}
		got, _ = e.run(store.Item{Method: "POST", URL: jsr(e.srv.URL + "/f"), Body: jsr(mm{"mode": "formdata", "formdata": []mm{{"key": "k", "value": v, "type": "text"}, {"key": "t", "value": v, "type": "text", "contentType": "text/plain"}}})})
		if len(got) != 1 {
			t.Errorf("formdata %q: %d requests", v, len(got))
			continue
		}
		parts := parseMultipart(t, got[0])
		if parts["k"] != v || parts["t"] != v {
			t.Errorf("multipart value %q arrived as k=%q t=%q", v, parts["k"], parts["t"])
		}
	}
}

// A form field NAME is data too: the pipeline percent-encodes it, so the script
// has to send the same pairs and never let a name add a parameter.
func TestExecFormFieldNamesArriveUnchanged(t *testing.T) {
	e := newExecEnv(t)
	for _, k := range []string{"a b", "x&admin=1", "a=b", "@/etc/hosts", "na+me", "100%", "ü"} {
		got, _ := e.run(store.Item{Method: "POST", URL: jsr(e.srv.URL + "/f"), Body: jsr(mm{"mode": "urlencoded", "urlencoded": []mm{{"key": k, "value": "v"}}})})
		if len(got) != 1 {
			t.Errorf("name %q: %d requests", k, len(got))
			continue
		}
		if form := parseForm(got[0].Body); len(form) != 1 || form[0] != [2]string{k, "v"} {
			t.Errorf("urlencoded name %q arrived as %q (%v)", k, got[0].Body, form)
		}
	}
}

func TestExecURLsArriveUnchanged(t *testing.T) {
	e := newExecEnv(t)
	for _, tail := range []string{
		"/p?a=1&b=2",
		"/p?q=it's&x=$(touch%20" + e.marker + ")",
		"/p?ids[]=1&ids[]=2",
		"/p?filter[name]=x&range=[1-3]",
		"/p?set={a,b}&n=1",
		"/p/a%20b;c=d?x=%41",
		"/p?q=`id`&r=$HOME",
	} {
		got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + tail)})
		if len(got) != 1 || got[0].URI != tail {
			uris := []string{}
			for _, g := range got {
				uris = append(uris, g.URI)
			}
			t.Errorf("URL tail %q arrived as %d request(s): %q", tail, len(got), uris)
		}
	}
}

func TestExecAuthArrivesUnchanged(t *testing.T) {
	e := newExecEnv(t)
	for _, v := range e.hostile() {
		if strings.ContainsAny(v, "\n") {
			continue
		}
		got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + "/a"), Auth: jsr(mm{"type": "bearer", "bearer": []mm{{"key": "token", "value": v}}})})
		if len(got) != 1 || got[0].Header.Get("Authorization") != "Bearer "+v {
			t.Errorf("bearer %q arrived as %+v", v, got)
		}
	}
	for _, v := range append(e.hostile(), "a&b=c#frag", "p%q", "sp ace") {
		got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + "/a?x=1"), Auth: jsr(mm{"type": "apikey", "apikey": []mm{{"key": "key", "value": "api key"}, {"key": "value", "value": v}, {"key": "in", "value": "query"}}})})
		if len(got) != 1 {
			t.Errorf("apikey %q: %d requests", v, len(got))
			continue
		}
		q := parseForm(strings.TrimPrefix(got[0].URI, "/a?"))
		if len(q) != 2 || q[0] != [2]string{"x", "1"} || q[1] != [2]string{"api key", v} {
			t.Errorf("api key value %q arrived as %q (%v)", v, got[0].URI, q)
		}
	}
	for _, v := range e.hostile() {
		if strings.ContainsAny(v, "\n") {
			continue
		}
		got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + "/a"), Auth: jsr(mm{"type": "basic", "basic": []mm{{"key": "username", "value": "user"}, {"key": "password", "value": v}}})})
		if len(got) != 1 {
			t.Errorf("basic %q: %d requests", v, len(got))
			continue
		}
		r := http.Request{Header: got[0].Header}
		if u, p, ok := r.BasicAuth(); !ok || u != "user" || p != v {
			t.Errorf("basic password %q arrived as %q:%q", v, u, p)
		}
	}
}

// curl treats "Name:" as "remove this header"; a header the request sends with
// an empty value needs the "Name;" form.
func TestExecEmptyHeaderIsSent(t *testing.T) {
	e := newExecEnv(t)
	got, _ := e.run(store.Item{Method: "GET", URL: jsr(e.srv.URL + "/h"), Headers: jsr([]mm{{"key": "X-Empty", "value": ""}, {"key": "X-After", "value": "1"}})})
	if len(got) != 1 {
		t.Fatalf("%d requests", len(got))
	}
	if v, ok := got[0].Header["X-Empty"]; !ok || len(v) != 1 || v[0] != "" || got[0].Header.Get("X-After") != "1" {
		t.Errorf("empty header was not sent: %v", got[0].Header)
	}
}

func TestExecNamesAndCommentsRunNothing(t *testing.T) {
	e := newExecEnv(t)
	name := "x\n touch " + e.marker + "\r\u2028touch " + e.marker + " \\"
	got, out := e.run(store.Item{Name: name, Method: "GET\n touch " + e.marker, URL: jsr(e.srv.URL + "/n"),
		Headers: jsr([]mm{{"key": "X-A", "value": "1\r\nX-Injected: 2"}})})
	if len(got) != 1 {
		t.Fatalf("%d requests\n%s", len(got), out.Data)
	}
	if got[0].Method != "GET" || got[0].Header.Get("X-Injected") != "" || got[0].Header.Get("X-A") != "1 X-Injected: 2" {
		t.Errorf("request line or headers were injected: %s %v", got[0].Method, got[0].Header)
	}
	feats := map[string]bool{}
	for _, w := range out.Warnings {
		feats[w.Feature] = true
	}
	if !feats["invalid-method"] || !feats["header-line-break"] {
		t.Errorf("warnings: %+v", out.Warnings)
	}
}

func parseForm(s string) [][2]string {
	var out [][2]string
	for _, p := range strings.Split(s, "&") {
		k, v, _ := strings.Cut(p, "=")
		out = append(out, [2]string{unesc(k), unesc(v)})
	}
	return out
}

func unesc(s string) string {
	u, err := url.QueryUnescape(s)
	if err != nil {
		return "!bad-escape:" + s
	}
	return u
}

func parseMultipart(t *testing.T, g gotReq) map[string]string {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://example.com/", strings.NewReader(g.Body))
	req.Header = g.Header
	out := map[string]string{}
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("multipart: %v", err)
		return out
	}
	for k, v := range req.MultipartForm.Value {
		out[k] = strings.Join(v, "|")
	}
	for k, fhs := range req.MultipartForm.File {
		for _, fh := range fhs {
			f, _ := fh.Open()
			b, _ := io.ReadAll(f)
			out[k] = string(b)
		}
	}
	return out
}
