package launcher

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Two launcher processes each hold their own Registry over the same file. An
// update from one must not be lost when the other saves its (older) view.
func TestRegistryTwoHandlesDoNotLoseUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	a, _ := Open(path)
	b, _ := Open(path)

	if err := a.Upsert(Instance{Project: "acme", PID: 11}); err != nil {
		t.Fatal(err)
	}
	if err := b.Upsert(Instance{Project: "beta", PID: 22}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := Open(path)
	if got := fresh.All(); len(got) != 2 {
		t.Fatalf("registry on disk = %+v, want both acme and beta", got)
	}
	// The other handle's entry is visible without reopening.
	if _, ok := a.Get("beta"); !ok {
		t.Error("handle a does not see the instance handle b registered")
	}

	// A removal through one handle is not resurrected by the other's next save.
	if err := a.Remove("acme"); err != nil {
		t.Fatal(err)
	}
	if err := b.Upsert(Instance{Project: "gamma", PID: 33}); err != nil {
		t.Fatal(err)
	}
	fresh, _ = Open(path)
	if _, ok := fresh.Get("acme"); ok {
		t.Error("acme was removed by one handle and written back by the other")
	}
	if len(fresh.All()) != 2 {
		t.Errorf("registry on disk = %+v, want beta and gamma", fresh.All())
	}
}

// Reconcile judges the entries that are on disk now, not the stale copy this
// handle loaded, and keeps another handle's live instance.
func TestRegistryReconcileKeepsOtherHandlesEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	a, _ := Open(path)
	b, _ := Open(path)
	_ = a.Upsert(Instance{Project: "dead", PID: 1})
	_ = b.Upsert(Instance{Project: "live", PID: 2})
	if err := a.Reconcile(func(pid int) bool { return pid == 2 }); err != nil {
		t.Fatal(err)
	}
	fresh, _ := Open(path)
	got := fresh.All()
	if len(got) != 1 || got[0].Project != "live" {
		t.Fatalf("after reconcile: %+v, want only live", got)
	}
}

func TestRegistryConcurrentHandlesKeepEveryInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	const handles, each = 4, 12
	var wg sync.WaitGroup
	for h := 0; h < handles; h++ {
		reg, _ := Open(path)
		wg.Add(1)
		go func(h int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := reg.Upsert(Instance{Project: fmt.Sprintf("p%d-%d", h, i), PID: 100 + i}); err != nil {
					t.Error(err)
				}
			}
		}(h)
	}
	wg.Wait()
	fresh, _ := Open(path)
	if got := len(fresh.All()); got != handles*each {
		t.Fatalf("%d instances on disk, want %d", got, handles*each)
	}
}
