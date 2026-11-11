// Package launcher tracks running per-project Interseptor instances so a
// single dashboard process can start/stop/discover them: the registry is a
// small JSON file (~/.interseptor/instances.json) mapping project name to
// {controlAddr, proxyAddr, pid}, written by whichever process spawns an
// instance and pruned lazily via Reconcile.
package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/proc"
)

// lockTimeout bounds the wait for another process's registry update. Updates
// take milliseconds and the kernel drops the lock of a process that died, so a
// longer wait means something is wrong; the caller then gets an error.
const lockTimeout = 5 * time.Second

// Instance is one running project's control-plane/proxy addresses and pid.
type Instance struct {
	Project     string `json:"project"`
	Dir         string `json:"dir,omitempty"`
	ControlAddr string `json:"controlAddr"`
	ProxyAddr   string `json:"proxyAddr"`
	PID         int    `json:"pid"`
	StartedAt   string `json:"startedAt,omitempty"`
}

// Registry is a mutex-guarded, disk-backed set of running instances keyed by
// project name. Safe for concurrent use, in one process and across processes:
// the file is shared by every launcher in the data dir, so each operation takes
// an exclusive lock on <path>.lock, re-reads the file, and (for a mutation)
// applies its change to that fresh state before publishing it. Without the
// re-read a second launcher would write its stale copy back and drop the
// instances the first one registered.
type Registry struct {
	path string

	mu        sync.Mutex
	byProject map[string]Instance
}

// Open loads path if it exists; a missing or corrupt file yields an empty,
// otherwise-usable registry rather than an error, since a stale/garbled
// registry shouldn't block the launcher from starting.
func Open(path string) (*Registry, error) {
	r := &Registry{path: path, byProject: map[string]Instance{}}
	if list, ok := readInstances(path); ok {
		r.byProject = list
	}
	return r, nil
}

// readInstances loads the registry file. ok is false when it is missing,
// unreadable or corrupt.
func readInstances(path string) (map[string]Instance, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var list []Instance
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, false
	}
	out := make(map[string]Instance, len(list))
	for _, inst := range list {
		out[inst.Project] = inst
	}
	return out, true
}

// syncLocked takes the cross-process lock and replaces the in-memory state with
// what is on disk; the caller holds r.mu and must call the returned unlock. If
// the file is missing or unreadable the in-memory state stands (a fresh data
// dir, or a registry whose last save failed). A lock that cannot be taken is
// returned as an error with the in-memory state untouched: reads then answer
// from memory, and mutations must not publish.
func (r *Registry) syncLocked() (unlock func(), err error) {
	lock, err := proc.LockFile(r.path+".lock", lockTimeout)
	if err != nil {
		return func() {}, err
	}
	if list, ok := readInstances(r.path); ok {
		r.byProject = list
	}
	return lock.Unlock, nil
}

// Get returns the recorded instance for project, if any.
func (r *Registry) Get(project string) (Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, _ := r.syncLocked()
	defer unlock()
	inst, ok := r.byProject[project]
	return inst, ok
}

// All returns every recorded instance, sorted by project name.
func (r *Registry) All() []Instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, _ := r.syncLocked()
	defer unlock()
	out := make([]Instance, 0, len(r.byProject))
	for _, inst := range r.byProject {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

// Upsert records/replaces inst and persists the registry.
func (r *Registry) Upsert(inst Instance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.syncLocked()
	defer unlock()
	r.byProject[inst.Project] = inst
	if err != nil {
		return err
	}
	return r.saveLocked()
}

// Remove drops project's entry (a no-op if absent) and persists the registry.
func (r *Registry) Remove(project string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.syncLocked()
	defer unlock()
	delete(r.byProject, project)
	if err != nil {
		return err
	}
	return r.saveLocked()
}

// RemoveIfPID drops project's entry only while it still records pid, so a late
// cleanup for an exited process cannot unregister a restarted instance.
func (r *Registry) RemoveIfPID(project string, pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.syncLocked()
	defer unlock()
	if inst, ok := r.byProject[project]; !ok || inst.PID != pid {
		return nil
	}
	delete(r.byProject, project)
	if err != nil {
		return err
	}
	return r.saveLocked()
}

// Reconcile drops entries whose process is no longer alive, as judged by
// isAlive (injected so tests don't depend on real OS process state).
func (r *Registry) Reconcile(isAlive func(pid int) bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.syncLocked()
	defer unlock()
	changed := false
	for name, inst := range r.byProject {
		if !isAlive(inst.PID) {
			delete(r.byProject, name)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err != nil {
		return err
	}
	return r.saveLocked()
}

// saveLocked persists the registry to disk while the caller holds r.mu and the
// cross-process lock. Keeping both through the atomic rename makes a completed
// Get/All observation a barrier for the corresponding disk write. The temp file
// has a unique name so a writer that died mid-save cannot leave a half-written
// file under a name the next writer renames into place.
func (r *Registry) saveLocked() error {
	list := make([]Instance, 0, len(r.byProject))
	for _, inst := range r.byProject {
		list = append(list, inst)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Project < list[j].Project })

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), "."+filepath.Base(r.path)+"-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), r.path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
