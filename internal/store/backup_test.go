package store

import (
	"os"
	"path/filepath"
	"testing"
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
