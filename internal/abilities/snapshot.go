package abilities

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// abilitySnapshot contains the exact bytes covered by its digest. Hidden paths
// are excluded both from the digest and from the available file namespace.
type abilitySnapshot struct {
	files  map[string][]byte
	paths  []string
	digest string
}

func snapshotDir(dir string) (abilitySnapshot, error) {
	if strings.TrimSpace(dir) == "" {
		return abilitySnapshot{}, errors.New("ability directory is empty")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return abilitySnapshot{}, err
	}
	defer root.Close()
	return snapshotRoot(root)
}

func snapshotRoot(root *os.Root) (abilitySnapshot, error) {
	snapshot := abilitySnapshot{files: make(map[string][]byte)}
	var totalBytes int64
	err := fs.WalkDir(root.FS(), ".", func(rel string, de fs.DirEntry, err error) error {
		if err != nil || rel == "." {
			return err
		}
		if de.Name() == ".DS_Store" || strings.HasPrefix(de.Name(), ".") {
			if de.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := validateRelPath(rel); err != nil {
			return err
		}
		if de.IsDir() {
			return nil
		}
		if len(snapshot.paths) >= MaxArchiveFiles {
			return fmt.Errorf("ability exceeds %d files", MaxArchiveFiles)
		}
		data, err := readSnapshotFile(root, rel, MaxAbilityBytes-totalBytes)
		if err != nil {
			return fmt.Errorf("read ability file %q: %w", rel, err)
		}
		totalBytes += int64(len(data))
		snapshot.paths = append(snapshot.paths, rel)
		snapshot.files[rel] = data
		return nil
	})
	if err != nil {
		return abilitySnapshot{}, err
	}

	sort.Strings(snapshot.paths)
	hash := sha256.New()
	var length [8]byte
	for _, rel := range snapshot.paths {
		data := snapshot.files[rel]
		binary.BigEndian.PutUint64(length[:], uint64(len(rel)))
		hash.Write(length[:])
		hash.Write([]byte(rel))
		binary.BigEndian.PutUint64(length[:], uint64(len(data)))
		hash.Write(length[:])
		hash.Write(data)
	}
	snapshot.digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return snapshot, nil
}

func readSnapshotFile(root *os.Root, rel string, budget int64) ([]byte, error) {
	name := filepath.FromSlash(rel)
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", MaxFileBytes)
	}
	if info.Size() > budget {
		return nil, fmt.Errorf("ability exceeds %d bytes", MaxAbilityBytes)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("file changed before reading")
	}
	limit := min(MaxFileBytes, budget)
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || int64(len(data)) != openedInfo.Size() {
		return nil, fmt.Errorf("file size changed while reading")
	}
	return data, nil
}

func (s abilitySnapshot) manifest() (AbilityManifest, error) {
	raw, ok := s.files[ManifestFileName]
	if !ok {
		return AbilityManifest{}, fmt.Errorf("%w: missing %s", ErrInvalidManifest, ManifestFileName)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return AbilityManifest{}, err
	}
	for _, ref := range manifestReferencedPaths(manifest) {
		if _, ok := s.files[ref]; !ok {
			return manifest, fmt.Errorf("%w: referenced file %q is missing or hidden", ErrInvalidManifest, ref)
		}
	}
	return manifest, nil
}

func (s abilitySnapshot) clone() abilitySnapshot {
	cp := abilitySnapshot{
		files:  make(map[string][]byte, len(s.files)),
		paths:  append([]string(nil), s.paths...),
		digest: s.digest,
	}
	for k, v := range s.files {
		b := make([]byte, len(v))
		copy(b, v)
		cp.files[k] = b
	}
	return cp
}

// openContainedAbilityRoot opens scopeDir/abilities/dirName via os.Root while
// refusing symlinks for both the abilities container and the ability directory.
func openContainedAbilityRoot(scopeDir, dirName string) (*os.Root, error) {
	if scopeDir == "" {
		return nil, errors.New("scope directory is empty")
	}
	if err := validateDirEntryName(dirName); err != nil {
		return nil, err
	}
	scopeRoot, err := os.OpenRoot(scopeDir)
	if err != nil {
		return nil, err
	}
	defer scopeRoot.Close()

	abInfo, err := scopeRoot.Lstat("abilities")
	if err != nil {
		return nil, err
	}
	if abInfo.Mode()&os.ModeSymlink != 0 || !abInfo.IsDir() {
		return nil, errors.New("abilities directory must be a regular directory, not a symlink")
	}
	abRoot, err := scopeRoot.OpenRoot("abilities")
	if err != nil {
		return nil, err
	}
	defer abRoot.Close()

	entInfo, err := abRoot.Lstat(dirName)
	if err != nil {
		return nil, err
	}
	if entInfo.Mode()&os.ModeSymlink != 0 || !entInfo.IsDir() {
		return nil, errors.New("ability directory must not be a symlink")
	}
	return abRoot.OpenRoot(dirName)
}

// ensureScopeAbilitiesRoot ensures scopeDir/abilities exists as a real (non-symlink)
// directory and returns an os.Root anchored at it along with its canonical OS path.
func ensureScopeAbilitiesRoot(scopeDir string) (*os.Root, string, error) {
	if scopeDir == "" {
		return nil, "", errors.New("scope directory is empty")
	}
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		return nil, "", err
	}
	canonScope, err := canonicalDir(scopeDir)
	if err != nil {
		return nil, "", err
	}
	scopeRoot, err := os.OpenRoot(canonScope)
	if err != nil {
		return nil, "", err
	}
	defer scopeRoot.Close()

	info, err := scopeRoot.Lstat("abilities")
	if errors.Is(err, fs.ErrNotExist) {
		if err := scopeRoot.Mkdir("abilities", 0o755); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, "", errors.New("abilities directory must be a regular directory, not a symlink")
	}

	abRoot, err := scopeRoot.OpenRoot("abilities")
	if err != nil {
		return nil, "", err
	}
	return abRoot, filepath.Join(canonScope, "abilities"), nil
}

func validateDirEntryName(name string) error {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("invalid ability directory name %q", name)
	}
	return validateRelPath(name)
}

// canonicalDir resolves symlinks (and resolves existing parent directories when
// the leaf does not yet exist) to produce a stable canonical absolute path.
func canonicalDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real), nil
	}
	var tail []string
	cur := abs
	for {
		parent := filepath.Dir(cur)
		base := filepath.Base(cur)
		if parent == cur {
			return abs, nil
		}
		tail = append(tail, base)
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			parts := make([]string, 0, 1+len(tail))
			parts = append(parts, realParent)
			for i := len(tail) - 1; i >= 0; i-- {
				parts = append(parts, tail[i])
			}
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		cur = parent
	}
}
