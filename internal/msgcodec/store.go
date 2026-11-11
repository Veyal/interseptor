package msgcodec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// validID constrains a codec id (file stem) to a safe slug.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidID reports whether id is a safe codec identifier.
func ValidID(id string) bool { return validID.MatchString(id) }

// Source is a stored codec with metadata and optional compile error.
type Source struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Meta   Meta   `json:"meta"`
	Error  string `json:"error,omitempty"`
}

// Exists reports whether <id>.star is present in dir.
func Exists(dir, id string) bool {
	if !ValidID(id) || dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, id+".star"))
	return err == nil
}

// List returns every *.star codec in dir (sorted).
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
		s := Source{ID: id, Source: string(src), Meta: Meta{ID: id, Title: id, Enabled: true}}
		if rerr != nil {
			s.Error = rerr.Error()
			out = append(out, s)
			continue
		}
		c, cerr := Compile(id, string(src))
		if cerr != nil {
			s.Error = cerr.Error()
		} else {
			s.Meta = c.Meta
		}
		out = append(out, s)
	}
	return out
}

// Read returns one codec's source.
func Read(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("invalid codec id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".star"))
	return string(b), err
}

// Save validates that src compiles, then writes <id>.star.
func Save(dir, id, src string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid codec id %q (use letters, digits, - or _)", id)
	}
	if _, err := Compile(id, src); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, id+".star"), []byte(src), 0o644)
}

// writeFileAtomic writes data to a temporary file beside path and renames it
// into place, so a crash or a concurrent reader sees the previous codec or the
// new one, never a truncated file. The temporary name does not end in .star,
// so LoadDir and List ignore one left behind by a crash.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		_ = os.Remove(tmpPath)
	}
	return err
}

// Delete removes a codec.
func Delete(dir, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid codec id %q", id)
	}
	return os.Remove(filepath.Join(dir, id+".star"))
}
