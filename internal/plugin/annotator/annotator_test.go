package annotator

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/plugin"
	"github.com/Veyal/interseptor/internal/store"
)

type fakeStore struct {
	mu     sync.Mutex
	flows  map[int64]*store.Flow
	tagged map[int64][]string
	getErr error
}

func (f *fakeStore) GetFlow(id int64) (*store.Flow, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flows[id], nil
}

func (f *fakeStore) AddFlowTags(id int64, tags []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tagged == nil {
		f.tagged = map[int64][]string{}
	}
	f.tagged[id] = append(f.tagged[id], tags...)
	return f.tagged[id], nil
}

func (f *fakeStore) tags(id int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tagged[id]...)
}

func TestAnnotateMatch(t *testing.T) {
	fs := &fakeStore{flows: map[int64]*store.Flow{1: {ID: 1, Host: "admin.internal.example.com"}}}
	annotate(fs, 1, []string{"internal"}, "internal-host")
	if got := fs.tags(1); len(got) != 1 || got[0] != "internal-host" {
		t.Fatalf("expected tag internal-host, got %v", got)
	}
}

func TestAnnotateNoMatch(t *testing.T) {
	fs := &fakeStore{flows: map[int64]*store.Flow{1: {ID: 1, Host: "www.example.com"}}}
	annotate(fs, 1, []string{"internal"}, "internal-host")
	if got := fs.tags(1); len(got) != 0 {
		t.Fatalf("expected no tags, got %v", got)
	}
}

func TestAnnotateGetFlowError(t *testing.T) {
	fs := &fakeStore{getErr: errors.New("gone")}
	annotate(fs, 99, []string{"internal"}, "x") // must not panic
	if got := fs.tags(99); len(got) != 0 {
		t.Fatalf("expected no tags on error, got %v", got)
	}
}

func TestEnableGuards(t *testing.T) {
	plugin.Reset()
	Enable(nil, Config{HostContains: []string{"x"}})           // nil store → no-op
	Enable(&fakeStore{}, Config{})                             // empty match → no-op
	Enable(&fakeStore{}, Config{HostContains: []string{"  "}}) // blank needle → no-op
	// None of the above should have registered a hook.
	fired := false
	plugin.OnFlowCaptured(func(int64) { fired = true })
	plugin.EmitFlowCaptured(1)
	if !fired {
		t.Fatal("sanity: registered hook did not fire")
	}
}

func TestEnableEndToEnd(t *testing.T) {
	plugin.Reset()
	fs := &fakeStore{flows: map[int64]*store.Flow{5: {ID: 5, Host: "api.INTERNAL.test"}}}
	Enable(fs, Config{HostContains: []string{"internal"}, Tag: "flagged"})
	plugin.EmitFlowCaptured(5) // fires the hook, which annotates in a goroutine

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := fs.tags(5); len(got) == 1 && got[0] == "flagged" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("flow was not tagged in time, got %v", fs.tags(5))
}
