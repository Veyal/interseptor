package launcher

import (
	"path/filepath"
	"testing"
)

// A late cleanup for an old process must not unregister a restarted instance
// of the same project.
func TestRemoveIfPIDOnlyRemovesMatchingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(Instance{Project: "demo", PID: 200}); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveIfPID("demo", 100); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get("demo"); !ok || got.PID != 200 {
		t.Fatalf("stale-PID remove dropped the live entry: %+v %v", got, ok)
	}
	if err := r.RemoveIfPID("demo", 200); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("demo"); ok {
		t.Fatal("matching-PID remove kept the entry")
	}
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Get("demo"); ok {
		t.Fatal("removal not persisted")
	}
}
