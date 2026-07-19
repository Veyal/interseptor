// Package vault is an always-on project archive store for multi-device / multi-
// operator sync. It holds full-project zips (same layout as control's export)
// under a configurable directory — typically served over Tailscale Serve.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ValidSlug reports whether id is a safe project vault identifier.
func ValidSlug(id string) bool { return validSlug.MatchString(id) }

// Store is a filesystem-backed vault root.
type Store struct {
	mu   sync.Mutex
	Dir  string
	Keep int // max revisions per project; <=0 means 10
}

type rootMeta struct {
	Keep      int   `json:"keep"`
	CreatedAt int64 `json:"createdAt"`
}

// ProjectInfo is a list-row for one vault project.
type ProjectInfo struct {
	ID           string `json:"id"`
	Revisions    int    `json:"revisions"`
	LatestRev    int    `json:"latestRev"`
	LatestSize   int64  `json:"latestSize"`
	LatestAt     int64  `json:"latestAt"`
	LatestLabel  string `json:"latestLabel,omitempty"`
	LatestSHA256 string `json:"latestSha256,omitempty"`
}

// RevInfo describes one stored revision.
type RevInfo struct {
	Rev       int    `json:"rev"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	At        int64  `json:"at"`
	Label     string `json:"label,omitempty"`
	Source    string `json:"source,omitempty"`
	Filename  string `json:"filename"`
}

func (s *Store) keep() int {
	if s.Keep <= 0 {
		return 10
	}
	return s.Keep
}

// Open initializes or loads a vault directory.
func Open(dir string, keep int) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("vault dir required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "projects"), 0o755); err != nil {
		return nil, err
	}
	st := &Store{Dir: abs, Keep: keep}
	metaPath := filepath.Join(abs, "meta.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		m := rootMeta{Keep: st.keep(), CreatedAt: time.Now().UnixMilli()}
		if err := writeJSONFile(metaPath, m); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func (s *Store) projectDir(id string) (string, error) {
	if !ValidSlug(id) {
		return "", fmt.Errorf("invalid project id %q", id)
	}
	return filepath.Join(s.Dir, "projects", id), nil
}

// Put stores r as a new revision of project id. Returns the revision metadata.
func (s *Store) Put(id, label, source string, r io.Reader) (RevInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pdir, err := s.projectDir(id)
	if err != nil {
		return RevInfo{}, err
	}
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		return RevInfo{}, err
	}

	next := s.nextRevLocked(pdir)
	tmp := filepath.Join(pdir, fmt.Sprintf(".tmp-rev-%06d.zip", next))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return RevInfo{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return RevInfo{}, err
	}
	if n == 0 {
		os.Remove(tmp)
		return RevInfo{}, fmt.Errorf("empty archive")
	}
	if err := validateProjectZip(tmp); err != nil {
		os.Remove(tmp)
		return RevInfo{}, err
	}
	final := filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", next))
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return RevInfo{}, err
	}
	info := RevInfo{
		Rev: next, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)),
		At: time.Now().UnixMilli(), Label: label, Source: source,
		Filename: filepath.Base(final),
	}
	if err := writeJSONFile(filepath.Join(pdir, fmt.Sprintf("rev-%06d.json", next)), info); err != nil {
		return RevInfo{}, err
	}
	_ = writeJSONFile(filepath.Join(pdir, "meta.json"), map[string]any{
		"id": id, "updatedAt": info.At, "latestRev": next,
	})
	s.pruneLocked(pdir)
	return info, nil
}

func (s *Store) nextRevLocked(pdir string) int {
	revs := s.listRevsLocked(pdir)
	if len(revs) == 0 {
		return 1
	}
	return revs[len(revs)-1].Rev + 1
}

func (s *Store) listRevsLocked(pdir string) []RevInfo {
	entries, err := os.ReadDir(pdir)
	if err != nil {
		return nil
	}
	var out []RevInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "rev-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var info RevInfo
		if err := readJSON(filepath.Join(pdir, name), &info); err != nil {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rev < out[j].Rev })
	return out
}

func (s *Store) pruneLocked(pdir string) {
	keep := s.keep()
	revs := s.listRevsLocked(pdir)
	if len(revs) <= keep {
		return
	}
	drop := revs[:len(revs)-keep]
	for _, r := range drop {
		_ = os.Remove(filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", r.Rev)))
		_ = os.Remove(filepath.Join(pdir, fmt.Sprintf("rev-%06d.json", r.Rev)))
	}
}

// List returns every project with latest revision summary.
func (s *Store) List() ([]ProjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.Dir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return []ProjectInfo{}, nil
		}
		return nil, err
	}
	var out []ProjectInfo
	for _, e := range entries {
		if !e.IsDir() || !ValidSlug(e.Name()) {
			continue
		}
		pdir := filepath.Join(s.Dir, "projects", e.Name())
		revs := s.listRevsLocked(pdir)
		pi := ProjectInfo{ID: e.Name(), Revisions: len(revs)}
		if len(revs) > 0 {
			last := revs[len(revs)-1]
			pi.LatestRev = last.Rev
			pi.LatestSize = last.Size
			pi.LatestAt = last.At
			pi.LatestLabel = last.Label
			pi.LatestSHA256 = last.SHA256
		}
		out = append(out, pi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Revisions lists revisions for a project (oldest first).
func (s *Store) Revisions(id string) ([]RevInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pdir, err := s.projectDir(id)
	if err != nil {
		return nil, err
	}
	revs := s.listRevsLocked(pdir)
	if len(revs) == 0 {
		if _, err := os.Stat(pdir); os.IsNotExist(err) {
			return nil, fmt.Errorf("project not found")
		}
	}
	return revs, nil
}

// OpenRev opens the zip for a revision (caller closes). rev<=0 means latest.
func (s *Store) OpenRev(id string, rev int) (io.ReadCloser, RevInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pdir, err := s.projectDir(id)
	if err != nil {
		return nil, RevInfo{}, err
	}
	revs := s.listRevsLocked(pdir)
	if len(revs) == 0 {
		return nil, RevInfo{}, fmt.Errorf("project not found")
	}
	var info RevInfo
	if rev <= 0 {
		info = revs[len(revs)-1]
	} else {
		found := false
		for _, r := range revs {
			if r.Rev == rev {
				info = r
				found = true
				break
			}
		}
		if !found {
			return nil, RevInfo{}, fmt.Errorf("revision %d not found", rev)
		}
	}
	f, err := os.Open(filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", info.Rev)))
	if err != nil {
		return nil, RevInfo{}, err
	}
	return f, info, nil
}

// DeleteRev removes one revision.
func (s *Store) DeleteRev(id string, rev int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pdir, err := s.projectDir(id)
	if err != nil {
		return err
	}
	if rev <= 0 {
		return fmt.Errorf("revision required")
	}
	_ = os.Remove(filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", rev)))
	if err := os.Remove(filepath.Join(pdir, fmt.Sprintf("rev-%06d.json", rev))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeleteProject removes a project and all revisions.
func (s *Store) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pdir, err := s.projectDir(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(pdir)
}

// Status returns vault root facts for /api/vault/status.
func (s *Store) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, _ := os.ReadDir(filepath.Join(s.Dir, "projects"))
	n := 0
	for _, e := range projects {
		if e.IsDir() && ValidSlug(e.Name()) {
			n++
		}
	}
	return map[string]any{
		"dir": s.Dir, "keep": s.keep(), "projects": n,
	}
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ParseRev parses a revision path segment.
func ParseRev(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid revision")
	}
	return n, nil
}
