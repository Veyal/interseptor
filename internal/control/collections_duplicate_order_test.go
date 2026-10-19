package control

import (
	"sort"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A duplicate lands right after its original, not at the end of the list, and
// the position persists (it is the stored rank, not a client-side reorder).
func TestDuplicateItemPlacedAfterOriginal(t *testing.T) {
	f := newCollFixture(t)
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Demo"}, asUI, 201, &co)
	mk := func(name string) store.Item {
		var it store.Item
		f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": name, "method": "GET", "url": "https://example.com/" + name}, asUI, 201, &it)
		return it
	}
	r1, _, _ := mk("R1"), mk("R2"), mk("R3")
	var dup store.Item
	f.must("POST", "/api/items/"+r1.UID+"/duplicate", nil, asUI, 201, &dup)
	var got struct {
		Items []store.Item `json:"items"`
	}
	f.must("GET", "/api/collections/"+co.UID, nil, asUI, 200, &got)
	sort.SliceStable(got.Items, func(i, j int) bool { return got.Items[i].Rank < got.Items[j].Rank })
	var names []string
	for _, it := range got.Items {
		names = append(names, it.Name)
	}
	want := []string{"R1", "R1 copy", "R2", "R3"}
	if len(names) != len(want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}
