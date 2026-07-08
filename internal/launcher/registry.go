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
)

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
// project name. Safe for concurrent use.
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
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return r, nil
	}
	var list []Instance
	if err := json.Unmarshal(b, &list); err != nil {
		return r, nil
	}
	for _, inst := range list {
		r.byProject[inst.Project] = inst
	}
	return r, nil
}

// Get returns the recorded instance for project, if any.
func (r *Registry) Get(project string) (Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.byProject[project]
	return inst, ok
}

// All returns every recorded instance, sorted by project name.
func (r *Registry) All() []Instance {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.byProject[inst.Project] = inst
	r.mu.Unlock()
	return r.save()
}

// Remove drops project's entry (a no-op if absent) and persists the registry.
func (r *Registry) Remove(project string) error {
	r.mu.Lock()
	delete(r.byProject, project)
	r.mu.Unlock()
	return r.save()
}

// Reconcile drops entries whose process is no longer alive, as judged by
// isAlive (injected so tests don't depend on real OS process state).
func (r *Registry) Reconcile(isAlive func(pid int) bool) error {
	r.mu.Lock()
	changed := false
	for name, inst := range r.byProject {
		if !isAlive(inst.PID) {
			delete(r.byProject, name)
			changed = true
		}
	}
	r.mu.Unlock()
	if !changed {
		return nil
	}
	return r.save()
}

// save persists the registry to disk, writing to a temp file first so a
// crash mid-write can't leave a truncated/corrupt registry behind.
func (r *Registry) save() error {
	r.mu.Lock()
	list := make([]Instance, 0, len(r.byProject))
	for _, inst := range r.byProject {
		list = append(list, inst)
	}
	r.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Project < list[j].Project })

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
