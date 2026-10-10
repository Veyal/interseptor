package store

import (
	"encoding/json"
	"fmt"
	"testing"
)

func sigBundle(coll string, items ...Item) CollectionsBundle {
	for i := range items {
		items[i].CollectionUID = coll
		if items[i].Kind == "" {
			items[i].Kind = "request"
		}
	}
	return CollectionsBundle{Version: CollectionsBundleVersion,
		Collections: []Collection{{UID: coll, Name: "sig"}}, Items: items}
}

func countItems(t *testing.T, s *Store) int {
	t.Helper()
	b, err := s.ExportCollectionsBundle(ScrubOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(b.Items)
}

// The same file re-imported with different JSON whitespace / key order must
// not duplicate every object-URL request.
func TestReimportWithDifferentWhitespaceDoesNotDuplicate(t *testing.T) {
	s := newTestStore(t)
	a := sigBundle("c1", Item{UID: "a1", Name: "r", Method: "GET", URL: json.RawMessage(`{"raw":"https://example.com/x?a=1","protocol":"https"}`)})
	b := sigBundle("c2", Item{UID: "b1", Name: "r", Method: "GET", URL: json.RawMessage(`{ "protocol" : "https",
	  "raw" : "https://example.com/x?a=1" }`)})
	if _, _, err := s.ImportUserCollectionsBundle(a); err != nil {
		t.Fatal(err)
	}
	st, skips, err := s.ImportUserCollectionsBundle(b)
	if err != nil || st.ItemsAdded != 0 || len(skips) != 1 || countItems(t, s) != 1 {
		t.Fatalf("stats %+v skips %v err %v items %d", st, skips, err, countItems(t, s))
	}
}

// Two requests in one folder that share name+method+URL but differ in body are
// different requests.
func TestSameNameMethodURLDifferentBodyAreKept(t *testing.T) {
	s := newTestStore(t)
	mk := func(coll, p string) CollectionsBundle {
		return sigBundle(coll,
			Item{UID: p + "1", Name: "login", Method: "POST", URL: json.RawMessage(`"https://example.com/l"`), Body: json.RawMessage(`{"mode":"raw","raw":"one"}`)},
			Item{UID: p + "2", Name: "login", Method: "POST", URL: json.RawMessage(`"https://example.com/l"`), Body: json.RawMessage(`{"mode":"raw","raw":"two"}`)})
	}
	st, skips, err := s.ImportUserCollectionsBundle(mk("c1", "a"))
	if err != nil || st.ItemsAdded != 2 || len(skips) != 0 {
		t.Fatalf("first import: %+v %v %v", st, skips, err)
	}
	st, skips, err = s.ImportUserCollectionsBundle(mk("c2", "b"))
	if err != nil || st.ItemsAdded != 0 || len(skips) != 2 || countItems(t, s) != 2 {
		t.Fatalf("re-import must be idempotent: %+v skips=%d items=%d %v", st, len(skips), countItems(t, s), err)
	}
}

// Identical requests inside one file are all kept on a fresh import, and a
// re-import matches them one to one.
func TestIdenticalRequestsInOneFileSurviveAndReimportIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	mk := func(coll, p string) CollectionsBundle {
		it := func(n string) Item {
			return Item{UID: p + n, Name: "ping", Method: "GET", URL: json.RawMessage(`"https://example.com/p"`)}
		}
		return sigBundle(coll, it("1"), it("2"), it("3"))
	}
	if st, _, err := s.ImportUserCollectionsBundle(mk("c1", "a")); err != nil || st.ItemsAdded != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	if st, _, err := s.ImportUserCollectionsBundle(mk("c2", "b")); err != nil || st.ItemsAdded != 0 || countItems(t, s) != 3 {
		t.Fatalf("%+v %v items=%d", st, err, countItems(t, s))
	}
}

// Same-named sibling folders stay separate, and their children land in the
// right one on import and on re-import.
func TestSameNamedSiblingFoldersStaySeparate(t *testing.T) {
	s := newTestStore(t)
	mk := func(coll, p string) CollectionsBundle {
		return sigBundle(coll,
			Item{UID: p + "f1", Kind: "folder", Name: "Auth"},
			Item{UID: p + "f2", Kind: "folder", Name: "Auth"},
			Item{UID: p + "r1", ParentUID: p + "f1", Name: "x", Method: "GET", URL: json.RawMessage(`"https://example.com/1"`)},
			Item{UID: p + "r2", ParentUID: p + "f2", Name: "x", Method: "GET", URL: json.RawMessage(`"https://example.com/1"`)})
	}
	if st, _, err := s.ImportUserCollectionsBundle(mk("c1", "a")); err != nil || st.ItemsAdded != 4 {
		t.Fatalf("first import %+v %v", st, err)
	}
	if st, _, err := s.ImportUserCollectionsBundle(mk("c2", "b")); err != nil || st.ItemsAdded != 0 || countItems(t, s) != 4 {
		t.Fatalf("re-import %+v %v items=%d", st, err, countItems(t, s))
	}
	b, _ := s.ExportCollectionsBundle(ScrubOptions{IncludeSecrets: true})
	kids := map[string]int{}
	for _, it := range b.Items {
		if it.Kind == "request" {
			kids[it.ParentUID]++
		}
	}
	if len(kids) != 2 {
		t.Fatalf("children must be spread over the two folders: %v", kids)
	}
}

// Skips are reported by name, not only counted.
func TestSkippedItemsAreNamed(t *testing.T) {
	s := newTestStore(t)
	it := func(p string) Item {
		return Item{UID: p, Name: "Get thing", Method: "GET", URL: json.RawMessage(`"https://example.com/t"`)}
	}
	s.ImportUserCollectionsBundle(sigBundle("c1", Item{UID: "fa", Kind: "folder", Name: "Dir"}, withParent(it("a1"), "fa")))
	_, skips, err := s.ImportUserCollectionsBundle(sigBundle("c2", Item{UID: "fb", Kind: "folder", Name: "Dir"}, withParent(it("b1"), "fb")))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range skips {
		names = append(names, k.Path)
	}
	if fmt.Sprint(names) != "[Dir/Get thing]" {
		t.Fatalf("skips %v", skips)
	}
}

func withParent(it Item, p string) Item { it.ParentUID = p; return it }
