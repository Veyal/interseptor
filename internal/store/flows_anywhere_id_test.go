package store

import (
	"strconv"
	"testing"
	"time"
)

// Regression: the History placeholder advertises #id, but the default
// "anywhere" scope rewrote the term into a text search before the id lookup
// could see it, so "#123" and "id:123" matched nothing.
func Test_SearchFlowsAnywhere_resolves_hash_and_id_prefix_to_the_flow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var ids []int64
	for i := 0; i < 3; i++ {
		id, insertErr := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https", Host: "example.com", Path: "/p", Status: 200})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		ids = append(ids, id)
	}
	for _, term := range []string{"#" + strconv.FormatInt(ids[1], 10), "id:" + strconv.FormatInt(ids[1], 10), "ID: " + strconv.FormatInt(ids[1], 10)} {
		got, _, searchErr := anywhereSearchIDs(s, FlowFilter{Search: term}, 0)
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if len(got) != 1 || got[0] != ids[1] {
			t.Fatalf("anywhere search %q = %v, want [%d]", term, got, ids[1])
		}
	}
}
