package rules

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// readStarDir reads every `*.star` file in dir, returning sorted ids and their
// bytes. A missing directory is not an error (a pack may be passive-only or
// active-only) — it yields empty slices.
func readStarDir(dir string) ([]string, [][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
			names = append(names, strings.TrimSuffix(e.Name(), ".star"))
		}
	}
	sort.Strings(names)
	files := make([][]byte, len(names))
	for i, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name+".star"))
		if err != nil {
			return nil, nil, err
		}
		files[i] = b
	}
	return names, files, nil
}
