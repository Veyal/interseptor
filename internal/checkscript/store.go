package checkscript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/starx"
)

// validID constrains a check id (its file stem) to a safe slug — no path
// separators or traversal — so ids can be mapped to files without escaping dir.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidID reports whether id is a safe check identifier.
func ValidID(id string) bool { return validID.MatchString(id) }

// Source is a stored check: its id, raw source, parsed metadata, and compile
// error (if any).
type Source struct {
	ID     string           `json:"id"`
	Source string           `json:"source"`
	Meta   starx.Metadata   `json:"meta,omitempty"`
	Error  string           `json:"error,omitempty"`
}

// List returns every `*.star` check in dir (sorted), each with its source,
// parsed metadata, and a compile error if it currently fails to compile. A
// missing dir yields nil.
func List(dir string) []Source {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]Source, 0, len(names))
	for _, name := range names {
		id := strings.TrimSuffix(name, ".star")
		src, rerr := os.ReadFile(filepath.Join(dir, name))
		s := Source{ID: id, Source: string(src), Meta: starx.ParseMetadata(string(src))}
		if rerr != nil {
			s.Error = rerr.Error()
		} else if _, cerr := Compile(id, string(src)); cerr != nil {
			s.Error = cerr.Error()
		}
		out = append(out, s)
	}
	return out
}

// Read returns one check's source.
func Read(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("invalid check id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".star"))
	return string(b), err
}

// Save validates that src compiles, then writes it as <id>.star (creating dir).
// A compile error is returned without writing, so a broken check never lands.
func Save(dir, id, src string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid check id %q (use letters, digits, - or _)", id)
	}
	if _, err := Compile(id, src); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".star"), []byte(src), 0o644)
}

// Delete removes a check.
func Delete(dir, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid check id %q", id)
	}
	return os.Remove(filepath.Join(dir, id+".star"))
}

// MergeDir copies *.star files from src into dst without overwriting files
// already in dst. A missing src dir is not an error. Returns how many were copied.
func MergeDir(src, dst string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	var n int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".star") {
			continue
		}
		dstPath := filepath.Join(dst, e.Name())
		if _, err := os.Stat(dstPath); err == nil {
			continue // keep existing global check
		} else if !os.IsNotExist(err) {
			return n, err
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
