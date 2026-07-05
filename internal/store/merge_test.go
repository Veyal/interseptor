package store

import (
	"path/filepath"
	"testing"
	"time"
)

// seedFlow inserts a flow (with a body) and returns its id.
func seedFlow(t *testing.T, s *Store, host, path, body string, tsMs int64) int64 {
	t.Helper()
	var hash string
	if body != "" {
		bw, err := s.NewBodyWriter()
		if err != nil {
			t.Fatalf("NewBodyWriter: %v", err)
		}
		bw.Write([]byte(body))
		hash, _, err = bw.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
	}
	id, err := s.InsertFlow(&Flow{
		TS: time.UnixMilli(tsMs), Method: "GET", Scheme: "https", Host: host, Port: 443,
		Path: path, Status: 200, ResBodyHash: hash, ResLen: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return id
}

func peerBodiesDir(s *Store) string { return s.BodiesDir() }

func TestMergeFromUnionsAndIsIdempotent(t *testing.T) {
	// Peer project with 2 flows + 1 finding referencing a flow.
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	f1 := seedFlow(t, peer, "victim.test", "/a", "resp-a", 1000)
	seedFlow(t, peer, "victim.test", "/b", "resp-b", 2000)
	body := marshalBody([]FindingBlock{
		{Type: "text", MD: "IDOR on /a"},
		{Type: "flow", FlowID: f1, Note: "poc"},
	})
	fid, err := peer.CreateFinding(&Finding{Severity: "High", Status: "verified", Source: "ai",
		Title: "IDOR", Target: "https://victim.test/a", Detail: "IDOR on /a", Body: body})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := peer.AttachFlow(fid, f1, "poc", -1); err != nil {
		t.Fatalf("AttachFlow: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, "interceptor.db")
	peerBodies := peerBodiesDir(peer)
	peer.Close()

	// Local project with 1 pre-existing flow.
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	seedFlow(t, local, "other.test", "/x", "resp-x", 500)

	// First merge: both peer flows + the finding land; body flow-ref remapped.
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != 2 || stats.FindingsAdded != 1 {
		t.Fatalf("first merge stats = %+v; want 2 flows / 1 finding added", stats)
	}
	flows, _ := local.QueryFlows(100)
	if len(flows) != 3 {
		t.Fatalf("expected 3 flows after merge, got %d", len(flows))
	}
	finds, _ := local.ListFindings("", "")
	if len(finds) != 1 {
		t.Fatalf("expected 1 merged finding, got %d", len(finds))
	}
	// The finding's PoC flow-ref must point at a LOCAL flow id that exists.
	f := finds[0]
	var pocFlowID int64
	for _, b := range f.Blocks {
		if b.Type == "flow" {
			pocFlowID = b.FlowID
		}
	}
	if pocFlowID == 0 {
		t.Fatal("merged finding lost its PoC flow block")
	}
	if _, err := local.GetFlow(pocFlowID); err != nil {
		t.Fatalf("remapped PoC flow id %d does not resolve locally: %v", pocFlowID, err)
	}

	// Second merge of the SAME peer: idempotent — nothing new added.
	stats2, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("second MergeFrom: %v", err)
	}
	if stats2.FlowsAdded != 0 || stats2.FindingsAdded != 0 {
		t.Fatalf("re-merge must be idempotent, got %+v", stats2)
	}
	if stats2.FlowsSkipped != 2 || stats2.FindingsSkipped != 1 {
		t.Fatalf("re-merge should skip all, got %+v", stats2)
	}
	if flows, _ := local.QueryFlows(100); len(flows) != 3 {
		t.Fatalf("re-merge must not duplicate flows, got %d", len(flows))
	}
}
