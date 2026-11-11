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
	Rev      int    `json:"rev"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	At       int64  `json:"at"`
	Label    string `json:"label,omitempty"`
	Source   string `json:"source,omitempty"`
	Filename string `json:"filename"`
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
	if err := os.MkdirAll(filepath.Join(abs, "projects"), 0o700); err != nil {
		return nil, err
	}
	removeStaleUploads(filepath.Join(abs, "projects"))
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
//
// The upload is received and validated into a temp file without the store
// lock: an archive can be gigabytes arriving over a slow link, and holding the
// lock for that long blocked every other vault operation, reads included. The
// lock is taken only to number the revision and publish it, so the revision
// number is assigned when the upload completes, not when it starts.
func (s *Store) Put(id, label, source string, r io.Reader) (RevInfo, error) {
	pdir, err := s.projectDir(id)
	if err != nil {
		return RevInfo{}, err
	}
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		return RevInfo{}, err
	}
	tmp, n, sum, err := receiveArchive(pdir, r)
	if err != nil {
		return RevInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitRevLocked(pdir, id, label, source, tmp, n, sum)
}

// receiveArchive streams r into a uniquely named temp file in pdir (the same
// directory, so publishing it is a rename) and validates it as a project
// archive. On error nothing is left behind.
func receiveArchive(pdir string, r io.Reader) (tmp string, n int64, sum string, err error) {
	f, err := os.CreateTemp(pdir, tmpRevPrefix+"*.zip") // created 0600
	if err != nil {
		return "", 0, "", err
	}
	tmp = f.Name()
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n == 0 {
		err = fmt.Errorf("empty archive")
	}
	if err == nil {
		err = validateProjectZip(tmp)
	}
	if err != nil {
		os.Remove(tmp)
		return "", 0, "", err
	}
	return tmp, n, hex.EncodeToString(h.Sum(nil)), nil
}

// tmpRevPrefix starts the name of an upload that has not been published.
const tmpRevPrefix = ".tmp-rev-"

// staleUploadAge is how old an unpublished upload must be before Open removes
// it. An upload is a single HTTP request, so a temp file this old was left by
// a process that died mid-upload.
const staleUploadAge = 24 * time.Hour

// removeStaleUploads deletes temp files left by uploads that never finished.
func removeStaleUploads(projects string) {
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasPrefix(f.Name(), tmpRevPrefix) {
				continue
			}
			if info, err := f.Info(); err == nil && time.Since(info.ModTime()) > staleUploadAge {
				_ = os.Remove(filepath.Join(projects, d.Name(), f.Name()))
			}
		}
	}
}

// commitRevLocked publishes a received archive as the project's next revision.
func (s *Store) commitRevLocked(pdir, id, label, source, tmp string, n int64, sum string) (RevInfo, error) {
	next := s.nextRevLocked(pdir)
	final := filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", next))
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return RevInfo{}, err
	}
	info := RevInfo{
		Rev: next, Size: n, SHA256: sum,
		At: time.Now().UnixMilli(), Label: label, Source: source,
		Filename: filepath.Base(final),
	}
	if err := writeJSONFile(filepath.Join(pdir, fmt.Sprintf("rev-%06d.json", next)), info); err != nil {
		if removeErr := os.Remove(final); removeErr != nil {
			return RevInfo{}, fmt.Errorf("write revision metadata: %w (remove archive: %v)", err, removeErr)
		}
		return RevInfo{}, fmt.Errorf("write revision metadata: %w", err)
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
	if err := os.Remove(filepath.Join(pdir, fmt.Sprintf("rev-%06d.zip", rev))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove revision archive: %w", err)
	}
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-json-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
