package abilities

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Veyal/interseptor/internal/plugin"
)

const (
	hookWorkerCount     = 2
	hookQueueCapacity   = 256
	maxActionConcurrent = 16
)

// Config configures a Manager over global and project ability directories.
type Config struct {
	GlobalDir  string
	ProjectDir string
	Store      FlowStore
	OnChange   func()
}

type loadedEntry struct {
	ability  Ability
	dirName  string
	snapshot abilitySnapshot
	hooks    []compiledFlowHook
	actions  map[string]compiledAction
}

type hookSnapshot struct {
	gen     uint64
	store   FlowStore
	hooks   []compiledFlowHook
	digests map[string]string
	ctx     context.Context
	cancel  context.CancelFunc
}

type flowHookJob struct {
	flowID int64
	snap   *hookSnapshot
}

// Manager discovers, verifies, trusts, serves assets for, and executes
// sandboxed abilities across global and project scopes.
type Manager struct {
	mu               sync.Mutex
	globalDir        string
	projectDir       string
	store            FlowStore
	onChange         func()
	entries          map[string]loadedEntry
	scoped           map[string]map[string]loadedEntry
	hookGen          atomic.Uint64
	hookSnap         atomic.Pointer[hookSnapshot]
	pluginRegistered atomic.Bool
	closeMu          sync.RWMutex
	unregPlugin      func()
	hookCh           chan flowHookJob
	hookWG           sync.WaitGroup
	workerWG         sync.WaitGroup
	actionSem        chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
	closed           atomic.Bool
	closeOnce        sync.Once
}

// NewManager creates a Manager and performs initial discovery from GlobalDir and ProjectDir.
func NewManager(cfg Config) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		globalDir:  cfg.GlobalDir,
		projectDir: cfg.ProjectDir,
		store:      cfg.Store,
		onChange:   cfg.OnChange,
		entries:    map[string]loadedEntry{},
		scoped:     map[string]map[string]loadedEntry{},
		hookCh:     make(chan flowHookJob, hookQueueCapacity),
		actionSem:  make(chan struct{}, maxActionConcurrent),
		ctx:        ctx,
		cancel:     cancel,
	}
	if err := m.Reload(); err != nil {
		cancel()
		return nil, err
	}
	for i := 0; i < hookWorkerCount; i++ {
		m.workerWG.Add(1)
		go m.hookWorker()
	}
	return m, nil
}

// Close unregisters the plugin hook, cancels in-flight hooks, drains queued
// jobs, and waits for hook workers to exit. Safe to call repeatedly.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		m.closeMu.Lock()
		unreg := m.unregPlugin
		m.unregPlugin = nil
		m.closed.Store(true)
		m.cancel()
		for {
			select {
			case <-m.hookCh:
				m.hookWG.Done()
			default:
				goto drained
			}
		}
	drained:
		snap := m.hookSnap.Load()
		m.closeMu.Unlock()

		if snap != nil && snap.cancel != nil {
			snap.cancel()
		}
		if unreg != nil {
			unreg()
		}
		m.workerWG.Wait()
		m.hookWG.Wait()
	})
}

// HasProjectScope reports whether a distinct project directory is configured.
// When ProjectDir aliases GlobalDir (such as the "default" project), only global
// ability storage is available.
func (m *Manager) HasProjectScope() bool {
	return m.projectDir != "" && (m.globalDir == "" || !sameDir(m.globalDir, m.projectDir))
}

// SetStore updates the FlowStore used by hooks and actions.
func (m *Manager) SetStore(st FlowStore) {
	m.mu.Lock()
	m.store = st
	m.publishHookSnapshotLocked()
	m.mu.Unlock()
}

// SetOnChange sets the callback invoked (outside locks) whenever ability state changes.
func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// Reload re-scans GlobalDir and ProjectDir and recomputes trust/drift status.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked()
}

// List returns deep copies of all discovered abilities sorted by ID.
func (m *Manager) List() []Ability {
	m.mu.Lock()
	out := make([]Ability, 0, len(m.entries))
	for _, ent := range m.entries {
		out = append(out, ent.ability.Clone())
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a deep copy of a single ability by ID.
func (m *Manager) Get(id string) (*Ability, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getLocked(id)
}

func (m *Manager) getLocked(id string) (*Ability, bool) {
	ent, ok := m.entries[id]
	if !ok {
		return nil, false
	}
	cp := ent.ability.Clone()
	return &cp, true
}

func (m *Manager) scopedLocked(scope, id string) (*Ability, bool) {
	byID := m.scoped[scope]
	if byID == nil {
		return nil, false
	}
	ent, ok := byID[id]
	if !ok {
		return nil, false
	}
	cp := ent.ability.Clone()
	return &cp, true
}

// Approve validates that ability id's active revision matches expectedDigest and
// expectedScope (re-verifying live disk contents before persisting), records
// approval in the machine-owned trust store, optionally enables it, reloads,
// and invokes OnChange outside the lock.
func (m *Manager) Approve(id, expectedDigest, expectedScope string, enabled bool) (*Ability, error) {
	m.mu.Lock()
	return m.approveLocked(id, expectedDigest, expectedScope, enabled)
}

// TrustAndEnable approves the ability's currently scanned digest and scope and
// optionally enables it. Intended for local CLI and test flows where the
// caller has already inspected the discovered ability.
func (m *Manager) TrustAndEnable(id string, enabled bool) (*Ability, error) {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return m.approveLocked(id, ent.ability.Digest, ent.ability.Scope, enabled)
}

func (m *Manager) approveLocked(id, expectedDigest, expectedScope string, enabled bool) (*Ability, error) {
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if ent.ability.LoadError != "" {
		msg := ent.ability.LoadError
		m.mu.Unlock()
		return nil, fmt.Errorf("cannot trust ability %q: %s", id, msg)
	}
	if ent.ability.Digest == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("cannot trust ability %q: missing digest", id)
	}
	if expectedScope == "" || ent.ability.Scope != expectedScope || expectedDigest == "" || ent.ability.Digest != expectedDigest {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDrift, id)
	}

	_, scopeDir, err := m.resolveScopeDir(ent.ability.Scope)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	// Re-verify live disk contents match the reviewed digest right before persisting trust.
	root, err := openContainedAbilityRoot(scopeDir, ent.dirName)
	if err != nil {
		_ = m.reloadLocked()
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", ErrDrift, err)
	}
	liveSnap, snapErr := snapshotRoot(root)
	_ = root.Close()
	if snapErr != nil || liveSnap.digest != expectedDigest {
		_ = m.reloadLocked()
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDrift, id)
	}

	states, err := loadTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("load trust state: %w", err)
	}
	states[id] = TrustState{
		TrustedDigest: expectedDigest,
		Enabled:       enabled,
		TrustedAt:     time.Now().UTC(),
	}
	if err := saveTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope, states); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	out, _ := m.getLocked(id)
	onChange := m.onChange
	if enabled && out != nil && out.LoadError != "" {
		// Top-level Starlark init failed at activation: keep disabled in persisted trust state.
		if st, exists := states[id]; exists {
			st.Enabled = false
			states[id] = st
			_ = saveTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope, states)
		}
		m.mu.Unlock()
		if onChange != nil {
			onChange()
		}
		return out, fmt.Errorf("cannot enable ability %q: %s", id, out.LoadError)
	}
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return out, nil
}

// RevokeTrust clears approval for ability id and disables it.
func (m *Manager) RevokeTrust(id string) (*Ability, error) {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if _, err := trustStatePathForScope(m.globalDir, m.projectDir, ent.ability.Scope); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := m.clearScopeTrustStateLocked(ent.ability.Scope, id); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	out, _ := m.getLocked(id)
	onChange := m.onChange
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return out, nil
}

// SetEnabled enables or disables a trusted ability. It refuses enabling when the
// ability is untrusted, has drifted on disk, or has a load error.
func (m *Manager) SetEnabled(id string, enabled bool) (*Ability, error) {
	return m.SetEnabledChecked(id, enabled, "", "")
}

// SetEnabledChecked toggles ability id after verifying optional expectedDigest
// and expectedScope against the active ability revision.
func (m *Manager) SetEnabledChecked(id string, enabled bool, expectedDigest, expectedScope string) (*Ability, error) {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if expectedScope != "" && ent.ability.Scope != expectedScope {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDrift, id)
	}
	if expectedDigest != "" && ent.ability.Digest != expectedDigest {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDrift, id)
	}
	if enabled {
		if ent.ability.LoadError != "" {
			msg := ent.ability.LoadError
			m.mu.Unlock()
			return nil, fmt.Errorf("cannot enable ability %q: %s", id, msg)
		}
		if ent.ability.Drift {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrDrift, id)
		}
		if !ent.ability.Trusted {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrNotTrusted, id)
		}
	}

	if _, err := trustStatePathForScope(m.globalDir, m.projectDir, ent.ability.Scope); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	states, err := loadTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("load trust state: %w", err)
	}
	if st, exists := states[id]; exists {
		st.Enabled = enabled
		states[id] = st
		if err := saveTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope, states); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	} else if enabled {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotTrusted, id)
	}

	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	out, _ := m.getLocked(id)
	onChange := m.onChange
	if enabled && out != nil && out.LoadError != "" {
		if st, exists := states[id]; exists {
			st.Enabled = false
			states[id] = st
			_ = saveTrustStatesForScope(m.globalDir, m.projectDir, ent.ability.Scope, states)
		}
		m.mu.Unlock()
		if onChange != nil {
			onChange()
		}
		return out, fmt.Errorf("cannot enable ability %q: %s", id, out.LoadError)
	}
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return out, nil
}

// Remove deletes an ability's directory and persisted trust entry, reloads,
// and notifies OnChange outside the lock.
func (m *Manager) Remove(id string) error {
	return m.RemoveChecked(id, "", "")
}

// RemoveChecked deletes ability id after verifying optional expectedScope and
// expectedDigest against the active revision.
func (m *Manager) RemoveChecked(id, expectedScope, expectedDigest string) error {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if expectedScope != "" && ent.ability.Scope != expectedScope {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDrift, id)
	}
	if expectedDigest != "" && ent.ability.Digest != expectedDigest {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDrift, id)
	}
	_, scopeDir, err := m.resolveScopeDir(ent.ability.Scope)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if err := validateDirEntryName(ent.dirName); err != nil {
		m.mu.Unlock()
		return err
	}

	scopeRoot, err := os.OpenRoot(scopeDir)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	abInfo, err := scopeRoot.Lstat("abilities")
	if err != nil {
		_ = scopeRoot.Close()
		m.mu.Unlock()
		return err
	}
	if abInfo.Mode()&os.ModeSymlink != 0 || !abInfo.IsDir() {
		_ = scopeRoot.Close()
		m.mu.Unlock()
		return errors.New("abilities directory must be a regular directory, not a symlink")
	}
	abRoot, err := scopeRoot.OpenRoot("abilities")
	_ = scopeRoot.Close()
	if err != nil {
		m.mu.Unlock()
		return err
	}

	entInfo, err := abRoot.Lstat(ent.dirName)
	if err != nil {
		_ = abRoot.Close()
		m.mu.Unlock()
		return err
	}
	if entInfo.Mode()&os.ModeSymlink != 0 {
		if err := abRoot.Remove(ent.dirName); err != nil {
			_ = abRoot.Close()
			m.mu.Unlock()
			return err
		}
	} else {
		if err := abRoot.RemoveAll(ent.dirName); err != nil {
			_ = abRoot.Close()
			m.mu.Unlock()
			return err
		}
	}
	_ = abRoot.Close()

	if err := m.clearScopeTrustStateLocked(ent.ability.Scope, id); err != nil {
		m.mu.Unlock()
		return err
	}
	if err := m.reloadLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	onChange := m.onChange
	m.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return nil
}

// ReadAsset returns immutable snapshot bytes and content-type for a UI asset
// belonging to a trusted, enabled ability matching the requested revision digest.
func (m *Manager) ReadAsset(id, relPath, revision string) ([]byte, string, bool) {
	if revision == "" {
		return nil, "", false
	}
	if err := validatePrefixedRelPath(relPath, "ui/"); err != nil {
		return nil, "", false
	}

	m.mu.Lock()
	ent, exists := m.entries[id]
	if !exists || !ent.ability.Trusted || !ent.ability.Enabled || ent.ability.Drift || ent.ability.LoadError != "" {
		m.mu.Unlock()
		return nil, "", false
	}
	if ent.snapshot.digest != revision {
		m.mu.Unlock()
		return nil, "", false
	}
	data, ok := ent.snapshot.files[relPath]
	if !ok {
		m.mu.Unlock()
		return nil, "", false
	}
	cp := bytes.Clone(data)
	m.mu.Unlock()
	return cp, assetContentType(relPath), true
}

func assetContentType(relPath string) string {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json", ".map":
		return "application/json"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".gif":
		return "image/gif"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// RegisterPluginHook wires HandleFlowCaptured into internal/plugin.RegisterFlowHook
// idempotently so Close() can cleanly remove the registration.
func (m *Manager) RegisterPluginHook() {
	if m == nil || m.closed.Load() {
		return
	}
	if m.pluginRegistered.CompareAndSwap(false, true) {
		unreg := plugin.RegisterFlowHook(m.HandleFlowCaptured)
		m.closeMu.Lock()
		if m.closed.Load() {
			m.closeMu.Unlock()
			unreg()
			return
		}
		m.unregPlugin = unreg
		m.closeMu.Unlock()
	}
}

// HandleFlowCaptured dispatches enabled flow.captured Starlark hooks asynchronously
// using a lock-free atomic snapshot and bounded worker queue so the proxy hot
// path never blocks on manager reloads or slow scripts.
func (m *Manager) HandleFlowCaptured(flowID int64) {
	if m == nil || flowID <= 0 || m.closed.Load() {
		return
	}
	snap := m.hookSnap.Load()
	if snap == nil || snap.store == nil || len(snap.hooks) == 0 {
		return
	}

	m.closeMu.RLock()
	if m.closed.Load() {
		m.closeMu.RUnlock()
		return
	}
	m.hookWG.Add(1)
	select {
	case m.hookCh <- flowHookJob{flowID: flowID, snap: snap}:
	default:
		m.hookWG.Done()
		log.Printf("abilities: hook queue full; dropping flow %d", flowID)
	}
	m.closeMu.RUnlock()
}

// WaitHooks blocks until all queued and in-flight asynchronous flow hooks finish.
func (m *Manager) WaitHooks() {
	if m != nil {
		m.hookWG.Wait()
	}
}

func (m *Manager) hookWorker() {
	defer m.workerWG.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-m.hookCh:
			m.processHookJob(job)
		}
	}
}

func (m *Manager) processHookJob(job flowHookJob) {
	defer m.hookWG.Done()
	if m.closed.Load() || job.snap == nil || job.snap.ctx.Err() != nil {
		return
	}
	cur := m.hookSnap.Load()
	if cur == nil || cur.gen != job.snap.gen {
		return
	}
	m.runFlowHooks(job.flowID, job.snap)
}

func (m *Manager) runFlowHooks(flowID int64, snap *hookSnapshot) {
	f, err := snap.store.GetFlow(flowID)
	if err != nil || f == nil {
		return
	}
	flowDict := flowToStarlark(snap.store, f)

	for _, h := range snap.hooks {
		if m.closed.Load() || snap.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(snap.ctx, 5*time.Second)
		tags, note, err := runFlowHook(ctx, h, flowDict)
		cancel()
		if err != nil {
			log.Printf("abilities: hook %s (%s) flow %d: %v", h.abilityID, h.entry, flowID, err)
			continue
		}
		if m.closed.Load() || snap.ctx.Err() != nil {
			return
		}
		if len(tags) > 0 {
			if _, err := snap.store.AddFlowTags(flowID, tags); err != nil {
				log.Printf("abilities: add tags from %s on flow %d: %v", h.abilityID, flowID, err)
			}
		}
		if note != "" {
			if err := snap.store.SetFlowNote(flowID, note); err != nil {
				log.Printf("abilities: set note from %s on flow %d: %v", h.abilityID, flowID, err)
			}
		}
	}
}

// RunAction executes a Starlark action from a trusted, enabled ability for an
// operator/UI caller.
func (m *Manager) RunAction(ctx context.Context, abilityID, actionID string, input map[string]any) (map[string]any, error) {
	return m.RunActionForCaller(ctx, abilityID, actionID, input, true)
}

// RunActionForCaller atomically verifies ability trust, enablement, action
// existence, and caller exposure (mcp:true when isUISession is false) on the
// same selected revision, then runs the action under bounded concurrency.
func (m *Manager) RunActionForCaller(ctx context.Context, abilityID, actionID string, input map[string]any, isUISession bool) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	ent, ok := m.entries[abilityID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, abilityID)
	}
	if ent.ability.LoadError != "" {
		msg := ent.ability.LoadError
		m.mu.Unlock()
		return nil, fmt.Errorf("ability %q has load error: %s", abilityID, msg)
	}
	if ent.ability.Drift {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDrift, abilityID)
	}
	if !ent.ability.Trusted || !ent.ability.Enabled {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s is not trusted and enabled", ErrNotTrusted, abilityID)
	}
	act, ok := ent.actions[actionID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("action %q not found in ability %q", actionID, abilityID)
	}
	if !isUISession && !act.spec.MCP {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: action %q in ability %q requires an interactive UI session", ErrActionNotExposed, actionID, abilityID)
	}
	fn := act.fn
	st := m.store
	m.mu.Unlock()

	select {
	case m.actionSem <- struct{}{}:
		defer func() { <-m.actionSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return runActionScript(ctx, abilityID, actionID, fn, input, st)
}

func (m *Manager) resolveScopeDir(scope string) (string, string, error) {
	switch scope {
	case "":
		if m.HasProjectScope() {
			return ScopeProject, m.projectDir, nil
		}
		if m.globalDir != "" {
			return ScopeGlobal, m.globalDir, nil
		}
		return "", "", errors.New("no ability scope directory configured")
	case ScopeGlobal:
		if m.globalDir == "" {
			return "", "", errors.New("global ability directory is not configured")
		}
		return ScopeGlobal, m.globalDir, nil
	case ScopeProject:
		if m.projectDir == "" {
			return "", "", errors.New("project ability directory is not configured")
		}
		if m.globalDir != "" && sameDir(m.globalDir, m.projectDir) {
			return "", "", errors.New("project ability scope is unavailable when project directory aliases global directory")
		}
		return ScopeProject, m.projectDir, nil
	default:
		return "", "", fmt.Errorf("invalid ability scope %q (want %q or %q)", scope, ScopeGlobal, ScopeProject)
	}
}

func (m *Manager) clearScopeTrustStateLocked(scope, id string) error {
	if scope == ScopeProject && m.globalDir == "" {
		return nil
	}
	states, err := loadTrustStatesForScope(m.globalDir, m.projectDir, scope)
	if err != nil {
		// Fail closed: clear corrupt file so newly installed ability is quarantined.
		return saveTrustStatesForScope(m.globalDir, m.projectDir, scope, map[string]TrustState{})
	}
	if _, exists := states[id]; !exists {
		return nil
	}
	delete(states, id)
	return saveTrustStatesForScope(m.globalDir, m.projectDir, scope, states)
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, errA := canonicalDir(a)
	cb, errB := canonicalDir(b)
	if errA == nil && errB == nil {
		return ca == cb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (m *Manager) reloadLocked() error {
	merged := map[string]loadedEntry{}
	scoped := map[string]map[string]loadedEntry{}

	type scopeTarget struct {
		scope string
		dir   string
	}
	var targets []scopeTarget
	if m.globalDir != "" {
		targets = append(targets, scopeTarget{scope: ScopeGlobal, dir: m.globalDir})
	}
	if m.HasProjectScope() {
		targets = append(targets, scopeTarget{scope: ScopeProject, dir: m.projectDir})
	}

	for _, t := range targets {
		states, stateErr := loadTrustStatesForScope(m.globalDir, m.projectDir, t.scope)
		entries, err := scanScopeAbilities(t.scope, t.dir, states, stateErr)
		if err != nil {
			return err
		}
		scopeMap := make(map[string]loadedEntry, len(entries))
		for _, ent := range entries {
			scopeMap[ent.ability.ID] = ent
			_, overrides := merged[ent.ability.ID]
			if t.scope == ScopeProject && overrides {
				ent.ability.OverridesGlobal = true
			}
			merged[ent.ability.ID] = ent
		}
		scoped[t.scope] = scopeMap
	}

	m.entries = merged
	m.scoped = scoped
	m.publishHookSnapshotLocked()
	return nil
}

func (m *Manager) publishHookSnapshotLocked() {
	prev := m.hookSnap.Load()
	gen := m.hookGen.Add(1)

	var hooks []compiledFlowHook
	digests := map[string]string{}
	for _, ent := range m.entries {
		if ent.ability.Trusted && ent.ability.Enabled && !ent.ability.Drift && ent.ability.LoadError == "" && len(ent.hooks) > 0 {
			hooks = append(hooks, ent.hooks...)
			digests[ent.ability.ID] = ent.snapshot.digest
		}
	}
	sort.Slice(hooks, func(i, j int) bool {
		if hooks[i].abilityID != hooks[j].abilityID {
			return hooks[i].abilityID < hooks[j].abilityID
		}
		return hooks[i].entry < hooks[j].entry
	})

	snapCtx, snapCancel := context.WithCancel(m.ctx)
	m.hookSnap.Store(&hookSnapshot{
		gen:     gen,
		store:   m.store,
		hooks:   hooks,
		digests: digests,
		ctx:     snapCtx,
		cancel:  snapCancel,
	})

	if prev != nil && prev.cancel != nil {
		if m.store == nil || hookSetRevoked(prev.digests, digests) {
			prev.cancel()
		}
	}
}

func hookSetRevoked(prev, next map[string]string) bool {
	for id, dig := range prev {
		if cur, ok := next[id]; !ok || cur != dig {
			return true
		}
	}
	return false
}

func scanScopeAbilities(scope, scopeDir string, states map[string]TrustState, stateErr error) ([]loadedEntry, error) {
	scopeRoot, err := os.OpenRoot(scopeDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer scopeRoot.Close()

	abInfo, err := scopeRoot.Lstat("abilities")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if abInfo.Mode()&os.ModeSymlink != 0 || !abInfo.IsDir() {
		return nil, fmt.Errorf("abilities directory in %s scope must not be a symlink", scope)
	}
	abRoot, err := scopeRoot.OpenRoot("abilities")
	if err != nil {
		return nil, err
	}
	defer abRoot.Close()

	dirEntries, err := fs.ReadDir(abRoot.FS(), ".")
	if err != nil {
		return nil, err
	}
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })

	canonScope, err := canonicalDir(scopeDir)
	if err != nil {
		canonScope = scopeDir
	}

	seenIDs := map[string]struct{}{}
	var out []loadedEntry
	for _, de := range dirEntries {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := abRoot.Lstat(name)
		if err != nil {
			continue
		}
		fullDir := filepath.Join(canonScope, "abilities", name)
		if info.Mode()&os.ModeSymlink != 0 {
			ent := loadedEntry{
				dirName: name,
				ability: Ability{
					ID:          name,
					Scope:       scope,
					Dir:         fullDir,
					Quarantined: true,
					Manifest:    AbilityManifest{ID: name, Name: name},
					LoadError:   "ability directory must not be a symlink",
				},
			}
			if _, dup := seenIDs[ent.ability.ID]; !dup {
				seenIDs[ent.ability.ID] = struct{}{}
				out = append(out, ent)
			}
			continue
		}
		if !info.IsDir() {
			continue
		}

		ent := loadAbilityEntry(abRoot, fullDir, name, scope, states, stateErr)
		if _, dup := seenIDs[ent.ability.ID]; dup {
			ent.ability.LoadError = fmt.Sprintf("duplicate ability id %q in %s scope", ent.ability.ID, scope)
			ent.ability.ID = name
			ent.ability.Trusted = false
			ent.ability.Enabled = false
			ent.ability.Quarantined = true
			ent.hooks = nil
			ent.actions = nil
			if _, dupDir := seenIDs[ent.ability.ID]; dupDir {
				continue
			}
		}
		seenIDs[ent.ability.ID] = struct{}{}
		out = append(out, ent)
	}
	return out, nil
}

func loadAbilityEntry(abRoot *os.Root, displayDir, dirName, scope string, states map[string]TrustState, stateErr error) loadedEntry {
	ab := Ability{
		ID:          dirName,
		Scope:       scope,
		Dir:         displayDir,
		Quarantined: true,
		Manifest:    AbilityManifest{ID: dirName, Name: dirName},
	}

	entryRoot, err := abRoot.OpenRoot(dirName)
	if err != nil {
		ab.LoadError = err.Error()
		return applyTrustState(ab, dirName, abilitySnapshot{}, nil, nil, states, stateErr, err)
	}
	snap, snapErr := snapshotRoot(entryRoot)
	_ = entryRoot.Close()
	ab.Digest = snap.digest

	var loadErr error
	var hookProgs []compiledProgram
	var actionProgs map[string]compiledProgram
	if snapErr != nil {
		loadErr = snapErr
	} else if manifest, mErr := snap.manifest(); mErr != nil {
		loadErr = mErr
	} else {
		ab.ID = manifest.ID
		ab.Manifest = manifest
		var cErr error
		hookProgs, actionProgs, cErr = compileAbilityPrograms(snap, ab.Manifest)
		if cErr != nil {
			loadErr = cErr
		}
	}

	if loadErr != nil {
		ab.LoadError = loadErr.Error()
	}

	return applyTrustState(ab, dirName, snap, hookProgs, actionProgs, states, stateErr, snapErr)
}

func applyTrustState(ab Ability, dirName string, snap abilitySnapshot, hookProgs []compiledProgram, actionProgs map[string]compiledProgram, states map[string]TrustState, stateErr, digestErr error) loadedEntry {
	if stateErr != nil {
		ab.Trusted = false
		ab.Enabled = false
		ab.Quarantined = true
		ab.TrustError = fmt.Sprintf("trust state unavailable: %v", stateErr)
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	st, exists := states[ab.ID]
	if !exists || st.TrustedDigest == "" {
		ab.Trusted = false
		ab.Enabled = false
		ab.Quarantined = true
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	ab.TrustedDigest = st.TrustedDigest
	ab.TrustedAt = st.TrustedAt

	if digestErr != nil || ab.Digest == "" || st.TrustedDigest != ab.Digest {
		ab.Drift = true
		ab.Trusted = false
		ab.Enabled = false
		ab.Quarantined = true
		ab.TrustError = "ability files changed on disk since approval"
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	if ab.LoadError != "" {
		ab.Trusted = false
		ab.Enabled = false
		ab.Quarantined = true
		ab.TrustError = "ability has load errors"
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	ab.Trusted = true
	ab.Enabled = st.Enabled
	ab.Quarantined = false
	if !ab.Enabled {
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	// Only trusted, enabled abilities evaluate Starlark module initializers.
	hooks, actions, initErr := initAbilityPrograms(ab.Manifest, hookProgs, actionProgs)
	if initErr != nil {
		ab.LoadError = initErr.Error()
		ab.Trusted = false
		ab.Enabled = false
		ab.Quarantined = true
		ab.TrustError = "ability has load errors"
		return loadedEntry{ability: ab, dirName: dirName, snapshot: snap}
	}

	return loadedEntry{
		ability:  ab,
		dirName:  dirName,
		snapshot: snap,
		hooks:    hooks,
		actions:  actions,
	}
}
