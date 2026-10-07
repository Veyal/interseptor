package collrun

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// fakeBackend scripts Step results by item name.
type fakeBackend struct {
	mu       sync.Mutex
	coll     store.Collection
	items    []store.Item
	reg      *redact.Registry
	trusted  map[string]bool
	step     func(in collexec.StepInput, call int) *collexec.StepResult
	calls    []stepCall
	commits  [][]collexec.VarChange
	baseVars map[string]string
}

type stepCall struct {
	Item   string
	Iter   int
	Layers []varstore.Layer
	Local  map[string]string
	Policy string
	NoScr  bool
	Meta   StepMeta
}

func newFake(names ...string) *fakeBackend {
	f := &fakeBackend{
		coll:    store.Collection{UID: "c1", Name: "Demo API", ScopePolicy: store.ScopePolicyBlock},
		reg:     redact.NewRegistry(),
		trusted: map[string]bool{},
	}
	rank := "a"
	for _, n := range names {
		f.items = append(f.items, store.Item{UID: "i-" + n, CollectionUID: "c1", Kind: "request", Name: n, Method: "GET", Rank: rank})
		rank += "a"
	}
	return f
}

func (f *fakeBackend) Load(string) (store.Collection, []store.Item, error) {
	return f.coll, f.items, nil
}
func (f *fakeBackend) EnvName(string) string { return "staging" }
func (f *fakeBackend) EnvPin(string) string  { return "" }
func (f *fakeBackend) Layers(chain collexec.Chain, _ string) ([]varstore.Layer, map[string]string, error) {
	vars := map[string]varstore.Var{}
	for k, v := range f.baseVars {
		vars[k] = varstore.Var{Value: v}
	}
	return []varstore.Layer{{Scope: varstore.ScopeEnvironment, Name: "env", Vars: vars}}, nil, nil
}
func (f *fakeBackend) Step(_ context.Context, in collexec.StepInput, meta StepMeta) (*collexec.StepResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, stepCall{Item: in.Chain.Item.Name, Iter: in.Iteration, Layers: in.Layers, Local: in.Local, Policy: in.ScopePolicy, NoScr: in.NoScripts, Meta: meta})
	n := len(f.calls)
	f.mu.Unlock()
	if f.step != nil {
		if r := f.step(in, n); r != nil {
			return r, nil
		}
	}
	return ok200(in.Chain.Item.Name), nil
}
func (f *fakeBackend) Commit(_ store.Collection, _ string, ch []collexec.VarChange) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, ch)
	return nil
}
func (f *fakeBackend) Trusted(_, hash string) bool { return f.trusted[hash] }
func (f *fakeBackend) Scrub(s string) string       { return redact.Text(f.reg.Mask(s)) }

func ok200(name string) *collexec.StepResult {
	return &collexec.StepResult{
		Outcome: collexec.OutcomeSent, Method: "GET", URL: "https://api.example.com/" + name, FlowID: 7,
		Response: &collexec.ResponseSummary{Status: 200, StatusText: "OK", Size: 12, TimeMs: 5},
		Tests:    []collexec.TestResult{{Name: "status 200", Status: collexec.TestPass}},
	}
}

func names(rep *Report) string {
	var out []string
	for _, it := range rep.Items {
		out = append(out, it.Name)
	}
	return strings.Join(out, ",")
}

func mustRun(t *testing.T, f *fakeBackend, rs RunStore, o Options) *Report {
	t.Helper()
	o.CollectionUID = "c1"
	rep, err := New(f, rs).Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}
