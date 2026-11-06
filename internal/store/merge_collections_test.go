package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func peerWithCollection(t *testing.T) (*Store, string) {
	t.Helper()
	peer := newTestStore(t)
	c := mustColl(t, peer, Collection{Name: "shared",
		Auth: json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"CANARY-MERGE-BEARER"}]}`)})
	mustItem(t, peer, Item{CollectionUID: c.UID, Kind: "request", Name: "r", Method: "GET",
		URL: json.RawMessage(`{"raw":"https://example.com/a"}`)})
	p := filepath.Join(t.TempDir(), "peer.db")
	if err := peer.BackupTo(p); err != nil {
		t.Fatal(err)
	}
	return peer, p
}

func TestMergeFromCarriesCollectionsAndReportsStats(t *testing.T) {
	_, peerDB := peerWithCollection(t)
	local := newTestStore(t)
	st, err := local.MergeFrom(peerDB, "", "peer")
	if err != nil {
		t.Fatal(err)
	}
	if st.Collections == nil || st.Collections.CollectionsAdded != 1 || st.Collections.ItemsAdded != 1 {
		t.Fatalf("collections stats = %+v", st.Collections)
	}
	cs, _ := local.ListCollections()
	if len(cs) != 1 || cs[0].Name != "shared" {
		t.Fatalf("collections not merged: %+v", cs)
	}
	// Scrub applies to the merged copy, and re-merge is idempotent.
	b, _ := local.ExportCollectionsBundle(ScrubOptions{IncludeSecrets: true})
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "CANARY-MERGE-BEARER") {
		t.Fatal("peer secret merged into local project")
	}
	st2, err := local.MergeFrom(peerDB, "", "peer")
	if err != nil {
		t.Fatal(err)
	}
	if st2.Collections == nil || st2.Collections.CollectionsAdded != 0 || st2.Collections.ItemsAdded != 0 {
		t.Fatalf("re-merge not idempotent: %+v", st2.Collections)
	}
}

func TestMergePreviewReportsCollectionsWithoutMutating(t *testing.T) {
	_, peerDB := peerWithCollection(t)
	local := newTestStore(t)
	st, err := local.MergePreview(peerDB, "", "peer")
	if err != nil {
		t.Fatal(err)
	}
	if st.Collections == nil || st.Collections.CollectionsAdded != 1 || st.Collections.ItemsAdded != 1 {
		t.Fatalf("preview collections stats = %+v", st.Collections)
	}
	if cs, _ := local.ListCollections(); len(cs) != 0 {
		t.Fatalf("preview mutated the store: %+v", cs)
	}
}

func TestMergeFromOlderPeerWithoutCollectionTables(t *testing.T) {
	local := newTestStore(t)
	peer := newTestStore(t) // never touched collections -> tables may be absent
	p := filepath.Join(t.TempDir(), "p.db")
	if err := peer.BackupTo(p); err != nil {
		t.Fatal(err)
	}
	if _, err := local.MergeFrom(p, "", "old"); err != nil {
		t.Fatal(err)
	}
}

// A peer collection that has been edited carries a higher rev than the copy this
// project created from it. That revision is the merge clock: it must be recorded
// so "higher rev wins" converges, otherwise every re-merge rewrites the
// collection (bumping its rev and updated timestamp again) instead of skipping
// it, and the local rev can never catch up with the peer's.
func TestMergeCollectionsConvergesOnPeerRevision(t *testing.T) {
	peer := newTestStore(t)
	c := mustColl(t, peer, Collection{Name: "shared"})
	for i := 0; i < 5; i++ {
		cur, err := peer.GetCollection(c.UID)
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if _, err := peer.UpdateCollection(*cur); err != nil {
			t.Fatalf("UpdateCollection: %v", err)
		}
	}
	peerColl, err := peer.GetCollection(c.UID)
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if peerColl.Rev < 6 {
		t.Fatalf("peer rev = %d, want it edited past the first revision", peerColl.Rev)
	}
	peerDB := filepath.Join(t.TempDir(), "peer.db")
	if err := peer.BackupTo(peerDB); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	local := newTestStore(t)
	if _, err := local.MergeFrom(peerDB, "", "peer"); err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	merged, err := local.ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(merged) != 1 || merged[0].Rev != peerColl.Rev {
		t.Fatalf("merged collection = %+v, want the peer revision %d recorded", merged, peerColl.Rev)
	}

	// Re-merging the same peer must not rewrite the collection again.
	st, err := local.MergeFrom(peerDB, "", "peer")
	if err != nil {
		t.Fatalf("re-merge: %v", err)
	}
	if st.Collections == nil || st.Collections.CollectionsUpdated != 0 || st.Collections.CollectionsSkipped != 1 {
		t.Fatalf("re-merge collections stats = %+v, want the collection skipped", st.Collections)
	}
	again, err := local.GetCollection(c.UID)
	if err != nil {
		t.Fatalf("GetCollection after re-merge: %v", err)
	}
	if again.Rev != peerColl.Rev {
		t.Fatalf("re-merge bumped the rev to %d, want it to stay at %d", again.Rev, peerColl.Rev)
	}
}

// A bundle may name a collection without a uid (the store assigns one on
// create). Its items then carry an empty collection uid too, so the merge must
// map them onto the uid CreateCollection actually assigned — not onto the empty
// string it was asked for, which no collection has.
func TestImportCollectionsBundleAssignsUIDToCollectionAndItsItems(t *testing.T) {
	s := newTestStore(t)
	bundle := CollectionsBundle{
		Version:     CollectionsBundleVersion,
		Collections: []Collection{{Name: "fresh"}},
		Items: []Item{{Kind: "request", Name: "ping", Method: "GET",
			URL: json.RawMessage(`{"raw":"https://example.com/ping"}`)}},
	}
	st, _, err := s.ImportUserCollectionsBundle(bundle)
	if err != nil {
		t.Fatalf("ImportUserCollectionsBundle: %v", err)
	}
	if st.CollectionsAdded != 1 || st.ItemsAdded != 1 || st.ItemsOrphaned != 0 {
		t.Fatalf("stats = %+v", st)
	}
	collections, err := s.ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(collections) != 1 || collections[0].UID == "" {
		t.Fatalf("collections = %+v, want one with an assigned uid", collections)
	}
	items, err := s.ListItems(collections[0].UID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Name != "ping" {
		t.Fatalf("items = %+v, want the imported request under the assigned uid", items)
	}
}
