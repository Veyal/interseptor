package chromemgr

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Every symlink below passes a purely lexical containment check, yet together
// they resolve outside the extraction directory: "sub/up" -> ".." is the
// archive root, and "sub/out" -> "up/.." is lexically "sub" but really the
// root's parent. A later member written "through" it must be refused.
func TestInstallRejectsWriteThroughSymlinkChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	parent := t.TempDir()
	dest := filepath.Join(parent, "chrome")
	srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{
		{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755},
		{name: "sub/up", body: "..", mode: os.ModeSymlink | 0o777},
		{name: "sub/out", body: "up/..", mode: os.ModeSymlink | 0o777},
		{name: "sub/out/pwned", body: "outside"},
	}))
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err == nil {
		t.Error("Install accepted an archive that writes through a symlink chain")
	}
	if _, err := os.Lstat(filepath.Join(parent, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("archive member escaped the install directory: %v", err)
	}
	if p, err := BinaryPath(dest, "linux", "amd64"); err == nil {
		t.Fatalf("BinaryPath = %q after a refused archive, want ErrNotInstalled", p)
	}
}

// A regular member must not be written through a symlink an earlier member
// created under the same name.
func TestInstallRejectsMemberReplacingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dest := filepath.Join(t.TempDir(), "chrome")
	srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{
		{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755},
		{name: "chrome-linux64/real", body: "original"},
		{name: "chrome-linux64/alias", body: "real", mode: os.ModeSymlink | 0o777},
		{name: "chrome-linux64/alias", body: "overwritten"},
	}))
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err == nil {
		t.Fatal("Install accepted a member that overwrites through an archive symlink")
	}
}
