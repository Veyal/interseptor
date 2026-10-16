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
