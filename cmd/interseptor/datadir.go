package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// oldDataDirName and newDataDirName are the pre- and post-rebrand names of
// Interseptor's per-user data directory (CA, projects, checks, instance
// registry, update-check cache — everything under the home-relative dir).
const (
	oldDataDirName = ".interceptor"
	newDataDirName = ".interseptor"
)

// migrateDataDir moves home/.interceptor to home/.interseptor the first time
// the renamed binary runs against an existing pre-rebrand install, so
// projects/CA/checks aren't orphaned. Safe to call on every startup — once
// the new directory exists (freshly migrated or freshly created) it is a
// no-op.
func migrateDataDir(home string) error {
	return migrateDir(filepath.Join(home, oldDataDirName), filepath.Join(home, newDataDirName))
}

// migrateDir moves oldDir to newDir:
//   - neither exists: no-op, nothing to migrate.
//   - only oldDir exists: renamed into newDir. A same-volume os.Rename is
//     tried first (atomic, fast); if that fails for any reason (e.g. oldDir
//     and newDir are on different volumes/devices) it falls back to a
//     recursive copy followed by removing oldDir.
//   - both exist: never merged or overwritten — migration is skipped and a
//     warning is logged so the operator can reconcile manually.
//
// Any unrecoverable failure is returned as a clear, wrapped error rather than
// being swallowed or left to crash the caller.
func migrateDir(oldDir, newDir string) error {
	oldInfo, err := os.Stat(oldDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // fresh install, nothing to migrate
		}
		return fmt.Errorf("migrate data dir: stat %s: %w", oldDir, err)
	}
	if !oldInfo.IsDir() {
		return fmt.Errorf("migrate data dir: %s exists but is not a directory", oldDir)
	}

	if _, err := os.Stat(newDir); err == nil {
		log.Printf("data dir migration skipped: %s already exists — leaving %s in place", newDir, oldDir)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("migrate data dir: stat %s: %w", newDir, err)
	}

	if err := os.Rename(oldDir, newDir); err == nil {
		log.Printf("migrated data directory %s -> %s", oldDir, newDir)
		return nil
	}

	// Rename failed (commonly EXDEV — oldDir/newDir on different volumes).
	// Fall back to a recursive copy, then remove the source.
	if err := copyDirRecursive(oldDir, newDir); err != nil {
		return fmt.Errorf("migrate data dir: copy %s to %s: %w", oldDir, newDir, err)
	}
	if err := os.RemoveAll(oldDir); err != nil {
		return fmt.Errorf("migrate data dir: copied %s to %s but could not remove the old directory: %w", oldDir, newDir, err)
	}
	log.Printf("migrated data directory %s -> %s (cross-device copy)", oldDir, newDir)
	return nil
}

func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
