package collexec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

type env struct {
	t    *testing.T
	st   *store.Store
	snd  *sender.Sender
	pipe *Pipeline
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	snd := sender.New(st, capture.New(st))
	e := &env{t: t, st: st, snd: snd}
	e.pipe = NewPipeline(Pipeline{Sender: snd, Flows: st, Bodies: st, Trust: st})
	return e
}

// fakeScope is an explicit allow-list.
type fakeScope struct{ hosts map[string]bool }

func (f fakeScope) HostInScope(h string) bool { return f.hosts[strings.ToLower(h)] }
func (f fakeScope) HasIncludes() bool         { return true }

func scopeOf1(hosts ...string) fakeScope {
	m := map[string]bool{}
	for _, h := range hosts {
		m[h] = true
	}
	return fakeScope{m}
}

func js(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type m = map[string]any

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

// chainOf builds a chain with one request item.
func chainOf(it store.Item) Chain {
	if it.UID == "" {
		it.UID = "item1"
	}
	if it.Kind == "" {
		it.Kind = "request"
	}
	return Chain{Collection: store.Collection{UID: "col1", Name: "C"}, Item: it}
}

func layer(sc varstore.Scope, vars map[string]string, secrets ...string) varstore.Layer {
	l := varstore.Layer{Scope: sc, Name: sc.String(), Vars: map[string]varstore.Var{}}
	for k, v := range vars {
		l.Vars[k] = varstore.Var{Value: v}
	}
	for _, k := range secrets {
		x := l.Vars[k]
		x.Secret = true
		l.Vars[k] = x
	}
	return l
}

// recorder is a test upstream capturing requests.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
	srv  *httptest.Server
}

func newRecorder(t *testing.T, h http.HandlerFunc) *recorder {
	t.Helper()
	r := &recorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b := new(strings.Builder)
		buf := make([]byte, 4096)
		for {
			n, err := q.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		r.mu.Lock()
		r.reqs = append(r.reqs, q)
		r.body = append(r.body, b.String())
		r.mu.Unlock()
		if h != nil {
			h(w, q)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"user":{"id":7,"name":"ann"},"items":[1,2,3]}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func (r *recorder) last() (*http.Request, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[len(r.reqs)-1], r.body[len(r.body)-1]
}

func (e *env) step(in StepInput) *StepResult {
	e.t.Helper()
	res, err := e.pipe.Step(context.Background(), in)
	if err != nil {
		e.t.Fatalf("Step: %v", err)
	}
	return res
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

type allTrusted struct{}

func (allTrusted) IsScriptTrusted(string, string) (bool, error) { return true, nil }

// events builds an events_json with one script for listen.
func events(listen string, lines ...string) json.RawMessage {
	return js([]m{{"listen": listen, "script": m{"exec": lines}}})
}
