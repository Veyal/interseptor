package control

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func writeFileAtomic(filename string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filename), "."+filepath.Base(filename)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFilePreservingDestination(tmpPath, filename)
}

// replaceFilePreservingDestination publishes a completed same-directory temp
// file without deleting an existing good destination first. The backup dance
// supports Windows, where os.Rename cannot replace an existing file.
func replaceFilePreservingDestination(tmpPath, dest string) error {
	backup := ""
	if info, err := os.Stat(dest); err == nil {
		if info.IsDir() {
			return fmt.Errorf("destination is a directory")
		}
		placeholder, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+"-backup-*.tmp")
		if err != nil {
			return err
		}
		backup = placeholder.Name()
		if err := placeholder.Close(); err != nil {
			_ = os.Remove(backup)
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("publish file: %v; restore previous destination: %w", err, restoreErr)
			}
		}
		return err
	}
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
}
