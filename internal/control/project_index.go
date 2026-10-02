package control

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// projectMeta is organizer data for the project picker. A category is a
// display folder such as "Clients/Acme"; it does not move the project
// directory or share that project's traffic with anything else.
type projectMeta struct {
	Category  string `json:"category,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	OpenedAt  int64  `json:"openedAt,omitempty"`
}

const maxProjectCategories = 3

var projectIndexMu sync.Mutex

func projectIndexPath(globalDir string) string {
	return filepath.Join(globalDir, "project-index.json")
}

func projectMetaKey(name, path string) string {
	if path != "" {
		return "path:" + filepath.Clean(path)
	}
	return "name:" + name
}

func readProjectIndex(globalDir string) map[string]projectMeta {
	if globalDir == "" {
		return map[string]projectMeta{}
	}
	b, err := os.ReadFile(projectIndexPath(globalDir))
	if err != nil {
		return map[string]projectMeta{}
	}
	var out map[string]projectMeta
	if json.Unmarshal(b, &out) != nil || out == nil {
		return map[string]projectMeta{}
	}
	return out
}

func writeProjectIndex(globalDir string, index map[string]projectMeta) error {
	if globalDir == "" {
		return errors.New("project storage location is not configured")
	}
	b, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(projectIndexPath(globalDir), append(b, '\n'), 0o644)
}

// touchProjectMeta records that key was opened and fills createdAt once from
// the project directory. Later opens keep the original created time.
func touchProjectMeta(globalDir, key, dir string, opened bool) error {
	if globalDir == "" || key == "" {
		return nil
	}
	projectIndexMu.Lock()
	defer projectIndexMu.Unlock()
	index := readProjectIndex(globalDir)
	meta := index[key]
	if meta.CreatedAt == 0 {
		meta.CreatedAt = fileCreatedUnix(dir)
	}
	if opened {
		meta.OpenedAt = time.Now().Unix()
	}
	index[key] = meta
	return writeProjectIndex(globalDir, index)
}

func setProjectCategory(globalDir, key, category string) error {
	if globalDir == "" || key == "" {
		return errors.New("project storage location is not configured")
	}
	projectIndexMu.Lock()
	defer projectIndexMu.Unlock()
	index := readProjectIndex(globalDir)
	if _, ok := index[key]; !ok && len(index) >= 500 {
		return errors.New("project index is full")
	}
	meta := index[key]
	meta.Category = category
	if meta.CreatedAt == 0 {
		meta.CreatedAt = time.Now().Unix()
	}
	index[key] = meta
	return writeProjectIndex(globalDir, index)
}

// ensureProjectCreated fills createdAt for projects the index has not seen.
// It does not change openedAt.
func ensureProjectCreated(globalDir string, keys map[string]string) map[string]projectMeta {
	if globalDir == "" {
		return map[string]projectMeta{}
	}
	projectIndexMu.Lock()
	defer projectIndexMu.Unlock()
	index := readProjectIndex(globalDir)
	dirty := false
	for key, dir := range keys {
		meta := index[key]
		if meta.CreatedAt != 0 {
			continue
		}
		meta.CreatedAt = fileCreatedUnix(dir)
		index[key] = meta
		dirty = true
	}
	if dirty {
		_ = writeProjectIndex(globalDir, index)
	}
	return index
}

func fileCreatedUnix(path string) int64 {
	if path == "" {
		return time.Now().Unix()
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Now().Unix()
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if sec := birthUnix(stat); sec > 0 {
			return sec
		}
	}
	if !info.ModTime().IsZero() {
		return info.ModTime().Unix()
	}
	return time.Now().Unix()
}

func normalizeProjectCategory(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) > maxProjectCategories {
		return "", errors.New("project folder can be at most 3 levels")
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return "", errors.New("project folder has an empty or parent segment")
		}
		if utf8.RuneCountInString(part) > 48 || strings.ContainsAny(part, "\r\n\x00") {
			return "", errors.New("project folder name is too long")
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.Join(out, "/"), nil
}
