package store

import (
	"fmt"
	"testing"
)

// A peer finding may link to another peer finding (structured relatedFindings).
// The link is stored by peer id, which has no meaning in this project: merging
// must remap it to the local id of the merged counterpart. Before the fix the
// unmapped peer id was validated as-is, so the merge either failed with
// "related finding #N does not exist" (blocking the whole project merge) or,
// when that id happened to exist locally, silently linked unrelated findings.
func TestMergeFromRemapsRelatedFindings(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	first, err := peer.CreateFinding(&Finding{Title: "first", Target: "https://related.test/first", Detail: "first detail"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := peer.CreateFinding(&Finding{Title: "second", Target: "https://related.test/second", Detail: "second detail"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	// Forward reference: the lower peer id links to the higher one.
	if err := peer.UpdateFindingMetadata(first, FindingMetadataPatch{
		RelatedFindings: &[]FindingRelation{{ID: second, Relation: "chain"}},
	}); err != nil {
		t.Fatalf("link findings: %v", err)
	}
	peerDBPath := peerDir + "/" + currentDBName
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	// Occupy local ids so a copied peer id cannot coincide with the right row.
	for i := 0; i < 5; i++ {
		if _, err := local.CreateFinding(&Finding{Title: "local filler", Target: "https://local.test/filler", Detail: "filler"}); err != nil {
			t.Fatalf("seed local finding: %v", err)
		}
	}

	if _, err := local.MergeFrom(peerDBPath, "", "peer"); err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}

	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	byTitle := map[string]Finding{}
	for _, f := range findings {
		byTitle[f.Title] = f
	}
	mergedFirst, ok := byTitle["first"]
	if !ok {
		t.Fatalf("merged finding %q missing", "first")
	}
	mergedSecond, ok := byTitle["second"]
	if !ok {
		t.Fatalf("merged finding %q missing", "second")
	}
	if mergedFirst.ID == first {
		t.Fatalf("merged finding kept the peer id %d", first)
	}
	if len(mergedFirst.RelatedFindings) != 1 {
		t.Fatalf("related findings = %+v, want the remapped chain", mergedFirst.RelatedFindings)
	}
	if got := mergedFirst.RelatedFindings[0]; got.ID != mergedSecond.ID || got.Relation != "chain" {
		t.Fatalf("related finding = %+v, want id %d chain", got, mergedSecond.ID)
	}

	// Re-merging is idempotent and keeps the link on the same local rows.
	stats, err := local.MergeFrom(peerDBPath, "", "peer")
	if err != nil {
		t.Fatalf("re-merge: %v", err)
	}
	if stats.FindingsAdded != 0 || stats.FindingsSkipped != 2 {
		t.Fatalf("re-merge stats = %+v, want both findings skipped", stats)
	}
	reloaded, err := local.GetFinding(mergedFirst.ID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if len(reloaded.RelatedFindings) != 1 || reloaded.RelatedFindings[0].ID != mergedSecond.ID {
		t.Fatalf("re-merge changed the relation: %+v", reloaded.RelatedFindings)
	}
}

// A relation pointing at a peer finding this project already has (so the peer
// row is skipped as a duplicate) must resolve to that existing local finding,
// not to a new row and not to the peer id.
func TestMergeFromRelatedFindingResolvesToDedupedCounterpart(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	// "shared" is identical in both projects, so the peer copy is deduped.
	sharedPeer, err := peer.CreateFinding(&Finding{Title: "shared", Target: "https://related.test/shared", Detail: "same detail"})
	if err != nil {
		t.Fatalf("create shared: %v", err)
	}
	carrier, err := peer.CreateFinding(&Finding{Title: "carrier", Target: "https://related.test/carrier", Detail: "carrier detail"})
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	if err := peer.UpdateFindingMetadata(carrier, FindingMetadataPatch{
		RelatedFindings: &[]FindingRelation{{ID: sharedPeer, Relation: "enables"}},
	}); err != nil {
		t.Fatalf("link findings: %v", err)
	}
	peerDBPath := peerDir + "/" + currentDBName
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	for i := 0; i < 3; i++ {
		if _, err := local.CreateFinding(&Finding{Title: "local filler", Target: "https://local.test/filler", Detail: "filler"}); err != nil {
			t.Fatalf("seed local finding: %v", err)
		}
	}
	sharedLocal, err := local.CreateFinding(&Finding{Title: "shared", Target: "https://related.test/shared", Detail: "same detail"})
	if err != nil {
		t.Fatalf("seed shared: %v", err)
	}

	stats, err := local.MergeFrom(peerDBPath, "", "peer")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FindingsAdded != 1 || stats.FindingsSkipped != 1 {
		t.Fatalf("merge stats = %+v, want one added and one skipped", stats)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	for _, f := range findings {
		if f.Title != "carrier" {
			continue
		}
		if len(f.RelatedFindings) != 1 {
			t.Fatalf("carrier related findings = %+v", f.RelatedFindings)
		}
		if got := f.RelatedFindings[0]; got.ID != sharedLocal || got.Relation != "enables" {
			t.Fatalf("carrier relation = %+v, want the deduped local id %d", got, sharedLocal)
		}
		return
	}
	t.Fatal("merged carrier finding missing")
}

// A peer link whose target id does not exist locally used to fail the whole
// merge (nothing was imported) because the copied peer id was validated as a
// local finding id.
func TestMergeFromUnmappedRelatedFindingDoesNotBlockTheMerge(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	carrier, err := peer.CreateFinding(&Finding{Title: "carrier", Target: "https://related.test/carrier", Detail: "carrier detail"})
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	var last int64
	lastTarget := ""
	for i := 0; i < 12; i++ {
		lastTarget = fmt.Sprintf("https://related.test/filler/%d", i)
		last, err = peer.CreateFinding(&Finding{Title: "peer filler", Target: lastTarget, Detail: fmt.Sprintf("filler %d", i)})
		if err != nil {
			t.Fatalf("create peer filler: %v", err)
		}
	}
	if err := peer.UpdateFindingMetadata(carrier, FindingMetadataPatch{
		RelatedFindings: &[]FindingRelation{{ID: last, Relation: "chain"}},
	}); err != nil {
		t.Fatalf("link findings: %v", err)
	}
	peerDBPath := peerDir + "/" + currentDBName
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.CreateFinding(&Finding{Title: "local filler", Target: "https://local.test/filler", Detail: "filler"}); err != nil {
		t.Fatalf("seed local finding: %v", err)
	}

	stats, err := local.MergeFrom(peerDBPath, "", "peer")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FindingsAdded != 13 {
		t.Fatalf("merge stats = %+v, want every peer finding imported", stats)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	byTitle := map[string]Finding{}
	for _, f := range findings {
		byTitle[f.Title] = f
	}
	mergedCarrier := byTitle["carrier"]
	if len(mergedCarrier.RelatedFindings) != 1 {
		t.Fatalf("carrier related findings = %+v", mergedCarrier.RelatedFindings)
	}
	var mergedTarget int64
	for _, f := range findings {
		if f.Title == "peer filler" && f.Target == lastTarget {
			mergedTarget = f.ID
			break
		}
	}
	if got := mergedCarrier.RelatedFindings[0]; got.ID != mergedTarget || got.Relation != "chain" {
		t.Fatalf("carrier relation = %+v, want remapped id %d", got, mergedTarget)
	}
}
