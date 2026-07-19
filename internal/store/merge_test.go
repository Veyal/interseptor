package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestMergeFromRejectsBodyWhoseContentDoesNotMatchFilename(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	var bodyPath string
	err = filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && len(d.Name()) == 64 {
			bodyPath = path
		}
		return err
	})
	if err != nil || bodyPath == "" {
		t.Fatalf("find peer body: path=%q err=%v", bodyPath, err)
	}
	if err := os.WriteFile(bodyPath, []byte("corrupt body"), 0o644); err != nil {
		t.Fatalf("corrupt body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("MergeFrom error = %v, want hash mismatch", err)
	}
	if flows, _ := local.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("corrupt merge mutated flows: %d", len(flows))
	}
}

func TestMergeFromDoesNotPublishBodiesBeforeAllCopiesValidate(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	corruptHash := strings.Repeat("f", 64)
	corruptPath := filepath.Join(peerBodies, "ff", "ff", corruptHash)
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o755); err != nil {
		t.Fatalf("mkdir corrupt body: %v", err)
	}
	if err := os.WriteFile(corruptPath, []byte("wrong"), 0o644); err != nil {
		t.Fatalf("write corrupt body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil {
		t.Fatal("MergeFrom accepted corrupt body")
	}
	var published []string
	_ = filepath.WalkDir(local.BodiesDir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			published = append(published, d.Name())
		}
		return err
	})
	if len(published) != 0 {
		t.Fatalf("merge published bodies before validation completed: %v", published)
	}
}

func TestMergeFromIgnoresActiveTempBodyFiles(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	if err := os.WriteFile(filepath.Join(peerBodies, ".tmp-active"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "peer")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != 1 || stats.BodiesAdded != 1 {
		t.Fatalf("stats=%+v, want one flow and body", stats)
	}
}

func TestMergeFromRejectsFlowReferencingMissingBody(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "required body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	if err := filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			return os.Remove(path)
		}
		return err
	}); err != nil {
		t.Fatalf("remove body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing body") {
		t.Fatalf("MergeFrom error=%v, want missing body", err)
	}
	if flows, _ := local.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("missing-body merge inserted %d flows", len(flows))
	}
}

func TestMergeFromFailsWhenRequiredBodyCannotBeRead(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "required body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	var bodyPath string
	_ = filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			bodyPath = path
		}
		return err
	})
	if bodyPath == "" {
		t.Fatal("body path not found")
	}
	if err := os.Remove(bodyPath); err != nil {
		t.Fatalf("remove body: %v", err)
	}
	if err := os.Symlink(filepath.Join(peerDir, "missing-target"), bodyPath); err != nil {
		t.Fatalf("create broken body symlink: %v", err)
	}
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil {
		t.Fatal("MergeFrom ignored unreadable required body")
	}
}

func TestMergeFromProtectsPublishedBodiesFromConcurrentGC(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "merge race body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()

	bodiesPublished := make(chan struct{})
	continueMerge := make(chan struct{})
	mergeDone := make(chan error, 1)
	go func() {
		_, err := local.mergeFrom(peerDBPath, peerBodies, "peer", mergeHooks{
			afterBodiesPublished: func() {
				close(bodiesPublished)
				<-continueMerge
			},
		})
		mergeDone <- err
	}()
	<-bodiesPublished
	removed, _, err := local.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed %d merge body before references committed", removed)
	}
	close(continueMerge)
	if err := <-mergeDone; err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
}

func TestConcurrentBodyPublishDeduplicatesRenameLoser(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "shared merge body", 1000)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()

	arrived := make(chan struct{}, 2)
	releaseRename := make(chan struct{})
	var renameMu sync.Mutex
	ops := bodyPublishOps{
		beforeRename: func(string) {
			arrived <- struct{}{}
			<-releaseRename
		},
		rename: func(oldPath, newPath string) error {
			renameMu.Lock()
			defer renameMu.Unlock()
			if _, err := os.Stat(newPath); err == nil {
				return os.ErrExist // emulate Windows rename-no-replace behavior
			}
			return os.Rename(oldPath, newPath)
		},
	}
	type result struct {
		added int
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			added, release, err := local.copyBodiesWithOps(peerBodies, ops)
			release()
			results <- result{added: added, err: err}
		}()
	}
	<-arrived
	<-arrived // both publishers observed destination absent
	close(releaseRename)

	totalAdded := 0
	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent publish: %v", res.err)
		}
		totalAdded += res.added
	}
	if totalAdded != 1 {
		t.Fatalf("total added=%d, want one published blob", totalAdded)
	}
}

func TestBodyPublishRenameLoserRejectsMismatchedDestination(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "expected merge body", 1000)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	ops := bodyPublishOps{
		beforeRename: func(dst string) {
			if err := os.WriteFile(dst, []byte("wrong destination content"), 0o644); err != nil {
				t.Errorf("write conflicting destination: %v", err)
			}
		},
		rename: func(string, string) error { return os.ErrExist },
	}
	_, release, err := local.copyBodiesWithOps(peerBodies, ops)
	release()
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("copyBodies error=%v, want destination hash mismatch", err)
	}
}

func TestMergeFromRejectsFindingReferencingMissingImageBody(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	hash, _, err := peer.PutImageBytes("image/png", []byte("image bytes"))
	if err != nil {
		t.Fatalf("PutImageBytes: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{Title: "Evidence", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := peer.AttachImage(findingID, hash, "image/png", "proof", -1); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}
	if err := os.Remove(peer.bodyPath(hash)); err != nil {
		t.Fatalf("remove image body: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("MergeFrom error=%v, want missing image body", err)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("missing-image merge inserted %d finding(s)", len(findings))
	}
}

func TestMergePreviewMatchesFirstMergeCounts(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	f1 := seedFlow(t, peer, "victim.test", "/a", "resp-a", 1000)
	seedFlow(t, peer, "victim.test", "/b", "resp-b", 2000)
	_, _ = peer.CreateFinding(&Finding{Severity: "High", Title: "IDOR", Target: "https://victim.test/a"})
	_ = f1
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peerBodiesDir(peer)
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	seedFlow(t, local, "other.test", "/x", "resp-x", 500)

	prev, err := local.MergePreview(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergePreview: %v", err)
	}
	if prev.FlowsAdded != 2 || prev.FindingsAdded != 1 {
		t.Fatalf("preview = %+v; want 2 flows / 1 finding", prev)
	}
	flows, _ := local.QueryFlows(100)
	if len(flows) != 1 {
		t.Fatalf("preview must not mutate; got %d flows", len(flows))
	}
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != prev.FlowsAdded || stats.FindingsAdded != prev.FindingsAdded {
		t.Fatalf("merge %+v != preview %+v", stats, prev)
	}
}

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
	peerDBPath := filepath.Join(peerDir, currentDBName)
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
	finds, _ := local.ListFindings("", "", "")
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
