package control

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A stray projects/default directory must not duplicate the reserved "default"
// entry (the root project) that availableProjects always lists first.
func TestAvailableProjectsSkipsReservedDefault(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"default", "beta", "acme"} {
		if err := os.MkdirAll(filepath.Join(tmp, "projects", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := (&projectAPI{&Hub{GlobalDir: tmp}}).availableProjects()
	want := []string{"default", "acme", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("availableProjects() = %v, want %v", got, want)
	}
}
