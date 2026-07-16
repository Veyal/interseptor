package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateDBFilenameRenamesLegacy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "interceptor.db"), []byte("main"), 0o644)
	os.WriteFile(filepath.Join(dir, "interceptor.db-wal"), []byte("wal"), 0o644)
	os.WriteFile(filepath.Join(dir, "interceptor.db-shm"), []byte("shm"), 0o644)

	if err := migrateDBFilename(dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, name := range []string{"interseptor.db", "interseptor.db-wal", "interseptor.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist after migration: %v", name, err)
		}
	}
	for _, name := range []string{"interceptor.db", "interceptor.db-wal", "interceptor.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected old %s to be gone, got %v", name, err)
		}
	}
}

func TestMigrateDBFilenameIdempotent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "interseptor.db"), []byte("existing"), 0o644)
	// new name already present + old absent → must be a clean no-op
	if err := migrateDBFilename(dir); err != nil {
		t.Fatalf("migrate should be a no-op: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "interseptor.db"))
	if string(got) != "existing" {
		t.Fatalf("existing db must not be touched, got %q", string(got))
	}
}

func TestMigrateDBFilenameNoLegacyIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := migrateDBFilename(dir); err != nil {
		t.Fatalf("migrate with no legacy db should be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "interseptor.db")); !os.IsNotExist(err) {
		t.Fatalf("nothing should have been created, got %v", err)
	}
}

// Open uses the new filename and transparently adopts a legacy-named DB.
func TestOpenMigratesLegacyDB(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bodies"), 0o755)
	// create a legacy DB via Open would name it interceptor.db pre-migration;
	// simulate by writing a non-empty file the migration will rename.
	os.WriteFile(filepath.Join(dir, "interceptor.db"), nil, 0o644)
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if _, err := os.Stat(filepath.Join(dir, "interseptor.db")); err != nil {
		t.Fatalf("Open should leave interseptor.db behind: %v", err)
	}
}
