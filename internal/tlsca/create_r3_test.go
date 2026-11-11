package tlsca

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A first run that cannot write the CA key (full disk, a crash between the
// two files) used to leave ca.crt behind on its own. Every later start then
// failed with "ca.crt present but ca.key unreadable" until the user deleted
// the file by hand. A failed create must leave nothing a later start trips on.
func TestR3FailedCreateDoesNotLeaveAHalfWrittenCA(t *testing.T) {
	dir := t.TempDir()
	// Make the key unwritable: a directory sits where ca.key belongs.
	if err := os.Mkdir(filepath.Join(dir, "ca.key"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("LoadOrCreate succeeded although ca.key could not be written")
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err == nil {
		t.Fatal("a failed create left ca.crt without a key")
	}
	// Once the obstacle is gone the next start creates a working CA.
	if err := os.Remove(filepath.Join(dir, "ca.key")); err != nil {
		t.Fatal(err)
	}
	ca, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("start after a failed create: %v", err)
	}
	if _, err := ca.LeafForHost("example.com"); err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreate(dir)
	if err != nil || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("reload: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("leftover files: %v", entries)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "ca.key"))
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("ca.key perms = %o, want 600", perm)
		}
	}
}
