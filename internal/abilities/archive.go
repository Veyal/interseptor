package abilities

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxTarStreamBytes = MaxAbilityBytes + int64(MaxArchiveFiles+64)*2048

type archiveEntry struct {
	rel   string
	isDir bool
	data  []byte
}

// InstallArchive validates and installs a .tar.gz or .zip ability archive into
// the requested scope ("global" or "project"). Installed abilities always start
// quarantined (Trusted: false, Enabled: false).
func (m *Manager) InstallArchive(r io.Reader, maxBytes int64, scope string) (*Ability, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidArchive)
	}
	scope, scopeDir, err := m.resolveScopeDir(scope)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxArchiveBytes
	}

	tmpArc, size, hdr, err := spoolArchive(r, maxBytes)
	if err != nil {
		return nil, err
	}
	defer func() {
		name := tmpArc.Name()
		_ = tmpArc.Close()
		_ = os.Remove(name)
	}()

	var rawEntries []archiveEntry
	switch {
	case len(hdr) >= 2 && hdr[0] == 0x1f && hdr[1] == 0x8b:
		rawEntries, err = readTarGzEntries(tmpArc)
	case len(hdr) >= 4 && hdr[0] == 'P' && hdr[1] == 'K' && hdr[2] == 0x03 && hdr[3] == 0x04:
		rawEntries, err = readZipEntries(tmpArc, size)
	default:
		return nil, fmt.Errorf("%w: unsupported archive format (expected .tar.gz or .zip)", ErrInvalidArchive)
	}
	if err != nil {
		return nil, err
	}

	entries, err := normalizeAndStripArchiveEntries(rawEntries)
	if err != nil {
		return nil, err
	}

	abRoot, abilitiesDir, err := ensureScopeAbilitiesRoot(scopeDir)
	if err != nil {
		return nil, err
	}
	defer abRoot.Close()

	stageDir, err := os.MkdirTemp(abilitiesDir, ".stage-*")
	if err != nil {
		return nil, err
	}
	stageName := filepath.Base(stageDir)
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = abRoot.RemoveAll(stageName)
		}
	}()

	stageRoot, err := abRoot.OpenRoot(stageName)
	if err != nil {
		return nil, err
	}
	if err := writeEntriesToRoot(stageRoot, entries); err != nil {
		_ = stageRoot.Close()
		return nil, err
	}
	snap, err := snapshotRoot(stageRoot)
	_ = stageRoot.Close()
	if err != nil {
		return nil, err
	}
	manifest, err := snap.manifest()
	if err != nil {
		return nil, err
	}
	if err := validateDirEntryName(manifest.ID); err != nil {
		return nil, err
	}
	if _, _, err := compileAbilityPrograms(snap, manifest); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if err := installDirAtomicRoot(abRoot, abilitiesDir, stageName, manifest.ID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	cleanupStage = false
	if err := m.clearScopeTrustStateLocked(scope, manifest.ID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	installed, ok := m.scopedLocked(scope, manifest.ID)
	onChange := m.onChange
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	if !ok {
		return nil, fmt.Errorf("ability %q installed in %s scope but missing after reload", manifest.ID, scope)
	}
	return installed, nil
}

func spoolArchive(r io.Reader, maxBytes int64) (*os.File, int64, []byte, error) {
	tmp, err := os.CreateTemp("", "interseptor-ability-*.arc")
	if err != nil {
		return nil, 0, nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()

	n, err := io.Copy(tmp, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%w: read archive: %v", ErrInvalidArchive, err)
	}
	if n > maxBytes {
		return nil, 0, nil, fmt.Errorf("%w: archive exceeds limit of %d bytes", ErrInvalidArchive, maxBytes)
	}
	if n < 4 {
		return nil, 0, nil, fmt.Errorf("%w: archive is empty or truncated", ErrInvalidArchive)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, nil, err
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(tmp, hdr); err != nil {
		return nil, 0, nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, nil, err
	}
	cleanup = false
	return tmp, n, hdr, nil
}

func readTarGzEntries(r io.Reader) ([]archiveEntry, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid gzip: %v", ErrInvalidArchive, err)
	}
	defer gz.Close()

	lr := &io.LimitedReader{R: gz, N: maxTarStreamBytes + 1}
	tr := tar.NewReader(lr)
	var entries []archiveEntry
	var totalBytes int64
	var seenEntries int
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if lr.N <= 0 {
				return nil, fmt.Errorf("%w: expanded tar stream exceeds %d bytes", ErrInvalidArchive, maxTarStreamBytes)
			}
			return nil, fmt.Errorf("%w: invalid tar: %v", ErrInvalidArchive, err)
		}
		seenEntries++
		if seenEntries > MaxArchiveFiles*2 {
			return nil, fmt.Errorf("%w: archive exceeds %d entries", ErrInvalidArchive, MaxArchiveFiles)
		}
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeRegA:
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("%w: symlinks and hardlinks are not allowed (%q)", ErrInvalidArchive, hdr.Name)
		default:
			return nil, fmt.Errorf("%w: unsupported tar entry type %q for %q", ErrInvalidArchive, hdr.Typeflag, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeDir && (hdr.Size < 0 || hdr.Size > MaxFileBytes) {
			return nil, fmt.Errorf("%w: entry %q exceeds %d bytes", ErrInvalidArchive, hdr.Name, MaxFileBytes)
		}

		rel, skip, err := sanitizeArchiveEntryPath(hdr.Name)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if len(entries) >= MaxArchiveFiles {
			return nil, fmt.Errorf("%w: archive exceeds %d entries", ErrInvalidArchive, MaxArchiveFiles)
		}
		if hdr.Typeflag == tar.TypeDir {
			entries = append(entries, archiveEntry{rel: rel, isDir: true})
			continue
		}
		data, err := readBoundedEntry(tr, rel, &totalBytes)
		if err != nil {
			if lr.N <= 0 {
				return nil, fmt.Errorf("%w: expanded tar stream exceeds %d bytes", ErrInvalidArchive, maxTarStreamBytes)
			}
			return nil, err
		}
		entries = append(entries, archiveEntry{rel: rel, data: data})
	}
	if lr.N <= 0 {
		return nil, fmt.Errorf("%w: expanded tar stream exceeds %d bytes", ErrInvalidArchive, maxTarStreamBytes)
	}
	return entries, nil
}

func readZipEntries(f *os.File, size int64) ([]archiveEntry, error) {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid zip: %v", ErrInvalidArchive, err)
	}
	if len(zr.File) > MaxArchiveFiles*2 {
		return nil, fmt.Errorf("%w: archive exceeds %d entries", ErrInvalidArchive, MaxArchiveFiles)
	}

	var entries []archiveEntry
	var totalBytes int64
	for _, zf := range zr.File {
		mode := zf.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symlinks are not allowed (%q)", ErrInvalidArchive, zf.Name)
		}
		if mode&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeCharDevice|os.ModeIrregular) != 0 {
			return nil, fmt.Errorf("%w: special files are not allowed (%q)", ErrInvalidArchive, zf.Name)
		}
		if !zf.FileInfo().IsDir() && zf.UncompressedSize64 > uint64(MaxFileBytes) {
			return nil, fmt.Errorf("%w: entry %q exceeds %d bytes", ErrInvalidArchive, zf.Name, MaxFileBytes)
		}

		rel, skip, err := sanitizeArchiveEntryPath(zf.Name)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if len(entries) >= MaxArchiveFiles {
			return nil, fmt.Errorf("%w: archive exceeds %d entries", ErrInvalidArchive, MaxArchiveFiles)
		}
		if zf.FileInfo().IsDir() {
			entries = append(entries, archiveEntry{rel: rel, isDir: true})
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: open %q: %v", ErrInvalidArchive, zf.Name, err)
		}
		data, readErr := readBoundedEntry(rc, rel, &totalBytes)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: close %q: %v", ErrInvalidArchive, zf.Name, closeErr)
		}
		entries = append(entries, archiveEntry{rel: rel, data: data})
	}
	return entries, nil
}

func readBoundedEntry(r io.Reader, rel string, totalBytes *int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidArchive, rel, err)
	}
	if int64(len(data)) > MaxFileBytes {
		return nil, fmt.Errorf("%w: entry %q exceeds %d bytes", ErrInvalidArchive, rel, MaxFileBytes)
	}
	if *totalBytes+int64(len(data)) > MaxAbilityBytes {
		return nil, fmt.Errorf("%w: expanded archive exceeds %d bytes", ErrInvalidArchive, MaxAbilityBytes)
	}
	*totalBytes += int64(len(data))
	return data, nil
}

func sanitizeArchiveEntryPath(raw string) (rel string, skip bool, err error) {
	if raw == "" {
		return "", false, fmt.Errorf("%w: empty entry path", ErrInvalidArchive)
	}
	if strings.ContainsAny(raw, "\\\x00") {
		return "", false, fmt.Errorf("%w: invalid characters in path %q", ErrInvalidArchive, raw)
	}
	if strings.HasPrefix(raw, "/") || (len(raw) >= 2 && raw[1] == ':') {
		return "", false, fmt.Errorf("%w: absolute path %q not allowed", ErrInvalidArchive, raw)
	}

	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" || trimmed == "." {
		return "", true, nil
	}
	segs := strings.Split(trimmed, "/")
	if segs[0] == "." {
		segs = segs[1:]
		if len(segs) == 0 {
			return "", true, nil
		}
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "", false, fmt.Errorf("%w: unsafe path %q", ErrInvalidArchive, raw)
		}
		if s == ".DS_Store" || strings.HasPrefix(s, ".") {
			return "", true, nil
		}
	}
	clean := strings.Join(segs, "/")
	if err := validateRelPath(clean); err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	return clean, false, nil
}

func normalizeAndStripArchiveEntries(entries []archiveEntry) ([]archiveEntry, error) {
	hasRootManifest := false
	for _, e := range entries {
		if !e.isDir && e.rel == ManifestFileName {
			hasRootManifest = true
			break
		}
	}
	if !hasRootManifest {
		top := ""
		uniformTop := len(entries) > 0
		hasNestedManifest := false
		for _, e := range entries {
			first, rest, hasSlash := strings.Cut(e.rel, "/")
			if first == "" {
				uniformTop = false
				break
			}
			if top == "" {
				top = first
			} else if first != top {
				uniformTop = false
				break
			}
			if hasSlash && rest == ManifestFileName && !e.isDir {
				hasNestedManifest = true
			}
		}
		if !uniformTop || !hasNestedManifest {
			return nil, fmt.Errorf("%w: missing %s", ErrInvalidArchive, ManifestFileName)
		}
		prefix := top + "/"
		stripped := make([]archiveEntry, 0, len(entries))
		for _, e := range entries {
			if e.rel == top {
				continue
			}
			e.rel = strings.TrimPrefix(e.rel, prefix)
			if e.rel == "" {
				continue
			}
			stripped = append(stripped, e)
		}
		entries = stripped
	}

	seenFiles := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if err := validateRelPath(e.rel); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		if e.isDir {
			continue
		}
		if _, dup := seenFiles[e.rel]; dup {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrInvalidArchive, e.rel)
		}
		seenFiles[e.rel] = struct{}{}
	}
	if _, ok := seenFiles[ManifestFileName]; !ok {
		return nil, fmt.Errorf("%w: missing %s", ErrInvalidArchive, ManifestFileName)
	}
	return entries, nil
}

func writeEntriesToRoot(root *os.Root, entries []archiveEntry) error {
	for _, e := range entries {
		target := filepath.FromSlash(e.rel)
		if e.isDir {
			if err := root.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if dir := filepath.Dir(target); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if err := root.WriteFile(target, e.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func installDirAtomicRoot(abRoot *os.Root, abilitiesDir, stageName, id string) error {
	backupName := ""
	if info, err := abRoot.Lstat(id); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("existing ability %q is a symlink", id)
		}
		tmpBackup, err := os.MkdirTemp(abilitiesDir, ".backup-"+id+"-*")
		if err != nil {
			return err
		}
		backupName = filepath.Base(tmpBackup)
		_ = abRoot.RemoveAll(backupName)
		if err := abRoot.Rename(id, backupName); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := abRoot.Rename(stageName, id); err != nil {
		if backupName != "" {
			if restoreErr := abRoot.Rename(backupName, id); restoreErr != nil {
				return fmt.Errorf("install ability %q: %v (restore failed: %w)", id, err, restoreErr)
			}
		}
		return err
	}
	if backupName != "" {
		_ = abRoot.RemoveAll(backupName)
	}
	return nil
}

// ExportArchive writes a deterministic .tar.gz archive for ability id with
// <id>/... entries directly from the verified immutable snapshot.
func (m *Manager) ExportArchive(id string, w io.Writer) error {
	if w == nil {
		return errors.New("writer is nil")
	}
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if ent.ability.ID == "" || strings.ContainsAny(ent.ability.ID, "/\\") {
		m.mu.Unlock()
		return fmt.Errorf("%w: invalid ability id %q", ErrInvalidManifest, ent.ability.ID)
	}
	if len(ent.snapshot.paths) == 0 {
		msg := ent.ability.LoadError
		m.mu.Unlock()
		if msg == "" {
			msg = "ability files unavailable for export"
		}
		return fmt.Errorf("%s: %s", id, msg)
	}
	snap := ent.snapshot.clone()
	abilityID := ent.ability.ID
	m.mu.Unlock()

	dirSet := map[string]struct{}{abilityID: {}}
	for _, rel := range snap.paths {
		for d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))); d != "." && d != ""; d = filepath.ToSlash(filepath.Dir(filepath.FromSlash(d))) {
			dirSet[abilityID+"/"+d] = struct{}{}
		}
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	gz := gzip.NewWriter(w)
	gz.Header.ModTime = time.Unix(0, 0).UTC()
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)

	fixedTime := time.Unix(0, 0).UTC()
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     d + "/",
			Mode:     0o755,
			Typeflag: tar.TypeDir,
			ModTime:  fixedTime,
			Format:   tar.FormatUSTAR,
		}); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
	}

	for _, rel := range snap.paths {
		if err := writeTarBytes(tw, abilityID+"/"+rel, snap.files[rel], fixedTime); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func writeTarBytes(tw *tar.Writer, tarName string, data []byte, modTime time.Time) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:     tarName,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  modTime,
		Format:   tar.FormatUSTAR,
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// Scaffold creates a complete starter ability directory on disk, installed
// quarantined, and reloads the manager.
func (m *Manager) Scaffold(req ScaffoldRequest) (*Ability, error) {
	if !ValidID(req.ID) {
		return nil, fmt.Errorf("%w: invalid ability id %q", ErrInvalidManifest, req.ID)
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	req.Description = strings.TrimSpace(req.Description)
	if len(req.Description) > MaxNoteLen || strings.ContainsRune(req.Description, 0) {
		return nil, fmt.Errorf("%w: invalid description", ErrInvalidManifest)
	}
	if req.Group == "" {
		req.Group = "abilities"
	}
	if !validGroup(req.Group) {
		return nil, fmt.Errorf("%w: invalid group %q", ErrInvalidManifest, req.Group)
	}

	scope, scopeDir, err := m.resolveScopeDir(req.Scope)
	if err != nil {
		return nil, err
	}
	if err := validateDirEntryName(req.ID); err != nil {
		return nil, err
	}

	abRoot, abilitiesDir, err := ensureScopeAbilitiesRoot(scopeDir)
	if err != nil {
		return nil, err
	}
	defer abRoot.Close()

	files, err := buildStarterAbilityFiles(req)
	if err != nil {
		return nil, err
	}

	stageDir, err := os.MkdirTemp(abilitiesDir, ".stage-*")
	if err != nil {
		return nil, err
	}
	stageName := filepath.Base(stageDir)
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = abRoot.RemoveAll(stageName)
		}
	}()

	stageRoot, err := abRoot.OpenRoot(stageName)
	if err != nil {
		return nil, err
	}
	if err := writeEntriesToRoot(stageRoot, files); err != nil {
		_ = stageRoot.Close()
		return nil, err
	}
	snap, err := snapshotRoot(stageRoot)
	_ = stageRoot.Close()
	if err != nil {
		return nil, err
	}
	manifest, err := snap.manifest()
	if err != nil {
		return nil, err
	}
	if _, _, err := compileAbilityPrograms(snap, manifest); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if _, err := abRoot.Lstat(req.ID); err == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s already exists in %s scope", ErrAlreadyExists, req.ID, scope)
	} else if !errors.Is(err, fs.ErrNotExist) {
		m.mu.Unlock()
		return nil, err
	}

	if err := abRoot.Rename(stageName, req.ID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	cleanupStage = false
	if err := m.clearScopeTrustStateLocked(scope, req.ID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ab, ok := m.scopedLocked(scope, req.ID)
	onChange := m.onChange
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	if !ok {
		return nil, fmt.Errorf("scaffolded ability %q missing after reload", req.ID)
	}
	return ab, nil
}

func buildStarterAbilityFiles(req ScaffoldRequest) ([]archiveEntry, error) {
	panelID := req.ID + "-panel"
	manifest := AbilityManifest{
		Schema:      SchemaV1,
		ID:          req.ID,
		Name:        req.Name,
		Version:     "0.1.0",
		Description: req.Description,
		Author:      strings.TrimSpace(req.Author),
		Menus: []MenuSpec{{
			ID:          panelID,
			Label:       req.Name,
			Group:       req.Group,
			Entry:       "ui/panel.js",
			Style:       "ui/style.css",
			Description: req.Description,
		}},
		ContextMenus: []ContextMenuSpec{{
			ID:     req.ID + "-inspect",
			Label:  "Inspect with " + req.Name,
			Target: "flow",
			Panel:  panelID,
			Action: "inspect",
		}},
		InspectorTabs: []InspectorTabSpec{{
			ID:    req.ID + "-tab",
			Label: req.Name,
			Side:  "both",
			Entry: "ui/inspector.js",
			Style: "ui/style.css",
		}},
		Commands: []CommandSpec{{
			ID:       req.ID + ":open",
			Title:    "Open " + req.Name,
			Keywords: []string{req.ID, req.Name},
			Panel:    panelID,
		}},
		Hooks: []HookSpec{{
			Event: EventFlowCaptured,
			Entry: "hooks/on_flow.star",
		}},
		Actions: []ActionSpec{{
			ID:          "inspect",
			Name:        "Inspect Flow",
			Description: "Run " + req.Name + " inspection against a flow or input.",
			Entry:       "hooks/action.star",
			MCP:         true,
		}},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')

	panelJS := fmt.Sprintf(`export function mount(root, ctx) {
  const wrap = document.createElement('section');
  wrap.className = 'ability-%s';

  const heading = document.createElement('h3');
  heading.textContent = %q;
  wrap.appendChild(heading);

  const summary = document.createElement('p');
  summary.textContent = %q;
  wrap.appendChild(summary);

  const pre = document.createElement('pre');
  pre.textContent = JSON.stringify(ctx || {}, null, 2);
  wrap.appendChild(pre);

  root.replaceChildren(wrap);
}

export function unmount(root) {
  if (root && typeof root.replaceChildren === 'function') {
    root.replaceChildren();
  }
}
`, req.ID, req.Name, req.Description)

	styleCSS := fmt.Sprintf(`.ability-%s {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
}

.ability-%s h3 {
  margin: 0;
  font-size: 14px;
}

.ability-%s pre {
  margin: 0;
  padding: 8px;
  border-radius: 6px;
  overflow: auto;
}
`, req.ID, req.ID, req.ID)

	inspectorJS := fmt.Sprintf(`export function mount(root, ctx) {
  const box = document.createElement('div');
  box.className = 'ability-%s';

  const title = document.createElement('strong');
  title.textContent = %q;
  box.appendChild(title);

  const pre = document.createElement('pre');
  pre.textContent = JSON.stringify((ctx && ctx.flow) || ctx || {}, null, 2);
  box.appendChild(pre);

  root.replaceChildren(box);
}

export function unmount(root) {
  if (root && typeof root.replaceChildren === 'function') {
    root.replaceChildren();
  }
}
`, req.ID, req.Name)

	onFlowStar := `# Flow hook executed asynchronously after capture settles.
# Return None or {"tags": ["tag-name"], "note": "note text"}.

def on_flow(flow):
    # Example:
    # if flow["host"] == "example.com" and flow["status"] >= 500:
    #     return {"tags": ["server-error"], "note": "5xx observed"}
    return None
`

	actionStar := fmt.Sprintf(`# Action invoked from UI, command palette, context menu, or MCP.

def run(ctx):
    flow = ctx.get("flow")
    return {
        "ability": %q,
        "hasFlow": flow != None,
        "host": flow["host"] if flow else "",
    }
`, req.ID)

	return []archiveEntry{
		{rel: ManifestFileName, data: bytes.Clone(manifestBytes)},
		{rel: "ui/panel.js", data: []byte(panelJS)},
		{rel: "ui/style.css", data: []byte(styleCSS)},
		{rel: "ui/inspector.js", data: []byte(inspectorJS)},
		{rel: "hooks/on_flow.star", data: []byte(onFlowStar)},
		{rel: "hooks/action.star", data: []byte(actionStar)},
	}, nil
}
