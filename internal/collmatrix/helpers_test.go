package collmatrix

import (
	"context"
	"sync"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// fakeBackend is a scripted collrun.Backend; respond decides each step.
type fakeBackend struct {
	mu      sync.Mutex
	coll    store.Collection
	items   []store.Item
	reg     *redact.Registry
	respond func(in collexec.StepInput) *collexec.StepResult
	calls   int
	layers  []varstore.Layer
}

func newFake(names ...string) *fakeBackend {
	f := &fakeBackend{
		coll: store.Collection{UID: "c1", Name: "Demo API", ScopePolicy: store.ScopePolicyBlock},
		reg:  redact.NewRegistry(),
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
func (f *fakeBackend) Layers(collexec.Chain, string) ([]varstore.Layer, map[string]string, error) {
	return f.layers, nil, nil
}
func (f *fakeBackend) Step(_ context.Context, in collexec.StepInput, _ collrun.StepMeta) (*collexec.StepResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.respond != nil {
		if r := f.respond(in); r != nil {
			return r, nil
		}
	}
	return stepResp(200, 100, 5, 1), nil
}
func (f *fakeBackend) Commit(store.Collection, string, []collexec.VarChange) []string { return nil }
func (f *fakeBackend) Trusted(string, string) bool                                    { return true }
func (f *fakeBackend) Scrub(s string) string                                          { return f.reg.Mask(s) }

func stepResp(status int, size, ms, flow int64) *collexec.StepResult {
	return &collexec.StepResult{
		Outcome: collexec.OutcomeSent, Method: "GET", URL: "https://api.example.com/x", FlowID: flow,
		Response: &collexec.ResponseSummary{Status: status, Size: size, TimeMs: ms},
	}
}
