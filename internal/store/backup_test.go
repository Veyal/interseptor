package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A snapshot produced by BackupTo must be a valid, self-contained database that
// re-opens and returns the same rows — the foundation of full-project export.
func TestBackupToRoundTrips(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, err := s.InsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "app.test", Path: "/x", Status: 200}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.BackupTo(dst); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		t.Fatalf("snapshot missing or empty: err=%v", err)
	}

	// Re-open the snapshot in a fresh store dir and confirm the row survived.
	restoreDir := t.TempDir()
	if err := os.Rename(dst, filepath.Join(restoreDir, "interceptor.db")); err != nil {
		t.Fatalf("place snapshot: %v", err)
	}
	s2, err := Open(restoreDir)
	if err != nil {
		t.Fatalf("Open snapshot: %v", err)
	}
	defer s2.Close()
	flows, err := s2.QueryFlowsFilter(FlowFilter{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(flows) != 1 || flows[0].Host != "app.test" {
		t.Fatalf("snapshot did not preserve flow: %+v", flows)
	}
}

// BackupTo must refuse to clobber an existing file.
func TestBackupToRefusesExisting(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	dst := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(dst, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.BackupTo(dst); err == nil {
		t.Fatalf("expected error backing up onto an existing file")
	}
}

// Capture is write-behind, so a flow the proxy has already handed to the store
// can still be sitting in the flusher's queue. A snapshot taken during live
// capture must include it instead of silently omitting the newest traffic.
func TestBackupToDrainsQueuedFlows(t *testing.T) {
	// Hold the flusher so the queued insert is provably uncommitted when the
	// backup starts; the snapshot itself must force it out.
	flowApplyDelayNS.Store(int64(500 * time.Millisecond))
	defer flowApplyDelayNS.Store(0)

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ok, err := s.EnqueueInsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "queued.example.test", Path: "/q", Status: 200})
	if err != nil || !ok {
		t.Fatalf("EnqueueInsertFlow: ok=%v err=%v", ok, err)
	}

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.BackupTo(dst); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	restoreDir := t.TempDir()
	if err := os.Rename(dst, filepath.Join(restoreDir, currentDBName)); err != nil {
		t.Fatalf("place snapshot: %v", err)
	}
	s2, err := Open(restoreDir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer s2.Close()
	flows, err := s2.QueryFlowsFilter(FlowFilter{Limit: 10})
	if err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if len(flows) != 1 || flows[0].Host != "queued.example.test" {
		t.Fatalf("snapshot dropped the queued flow: %+v", flows)
	}
}

// A capture summary taken during live traffic must count the flows the store has
// already accepted, not only the ones the flusher happened to commit. Report
// readiness and the storage breakdown read these totals as evidence.
func TestFlowCountAndHostStatsDrainQueuedFlows(t *testing.T) {
	flowApplyDelayNS.Store(int64(500 * time.Millisecond))
	defer flowApplyDelayNS.Store(0)

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if ok, err := s.EnqueueInsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "counted.example.test", Path: "/c", Status: 200, ResLen: 10}); !ok || err != nil {
		t.Fatalf("EnqueueInsertFlow: ok=%v err=%v", ok, err)
	}

	n, err := s.FlowCount()
	if err != nil {
		t.Fatalf("FlowCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("FlowCount = %d, want the queued flow counted", n)
	}
	stats, err := s.HostStats()
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Host != "counted.example.test" || stats[0].Flows != 1 {
		t.Fatalf("HostStats = %+v, want the queued flow counted", stats)
	}
}
