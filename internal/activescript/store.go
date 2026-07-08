package activescript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/activescan"
)

// validID constrains a check id (its file stem) to a safe slug — no path
// separators or traversal — so ids map to files without escaping the dir.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidID reports whether id is a safe active-check identifier.
func ValidID(id string) bool { return validID.MatchString(id) }

// Source is a stored active check: id, raw source, compile error (if any).
type Source struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Error  string `json:"error,omitempty"`
}

// List returns every `*.star` active check in dir (sorted), each with its source
// and a compile error if it currently fails to compile. A missing dir yields nil.
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
		s := Source{ID: id, Source: string(src)}
		if rerr != nil {
			s.Error = rerr.Error()
		} else if _, cerr := Compile(id, string(src)); cerr != nil {
			s.Error = cerr.Error()
		}
		out = append(out, s)
	}
	return out
}

// Read returns one active check's source.
func Read(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("invalid active check id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".star"))
	return string(b), err
}

// Save validates that src compiles, then writes it as <id>.star (creating dir).
func Save(dir, id, src string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid active check id %q (use letters, digits, - or _)", id)
	}
	if _, err := Compile(id, src); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".star"), []byte(src), 0o644)
}

// Delete removes an active check.
func Delete(dir, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid active check id %q", id)
	}
	return os.Remove(filepath.Join(dir, id+".star"))
}

// LoadDir compiles every `*.star` active check in dir (sorted). Returns the
// compiled checks plus a filename→error map for any that failed to compile.
func LoadDir(dir string) ([]*Check, map[string]error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var checks []*Check
	var errs map[string]error
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			c, cerr := Compile(strings.TrimSuffix(name, ".star"), string(src))
			if cerr == nil {
				checks = append(checks, c)
				continue
			}
			err = cerr
		}
		if errs == nil {
			errs = map[string]error{}
		}
		errs[name] = err
	}
	return checks, errs
}

// ToActiveChecks wraps compiled user checks as activescan.Check entries. When
// the file id matches a built-in probe id, the check replaces that built-in
// (same disable toggle). Otherwise ids are prefixed with custom-active:.
func ToActiveChecks(checks []*Check) []activescan.Check {
	out := make([]activescan.Check, 0, len(checks))
	for _, c := range checks {
		c := c
		ac := activescan.Check{Run: c.Run}
		if meta, ok := activescan.BuiltinMeta(c.ID); ok {
			ac.ID = c.ID
			ac.Class = meta.Class
			ac.Severity = meta.Severity
			ac.Title = meta.Title
			ac.Fix = meta.Fix
		} else {
			ac.ID = "custom-active:" + c.ID
			ac.Title = "Custom active check: " + c.ID
		}
		out = append(out, ac)
	}
	return out
}
