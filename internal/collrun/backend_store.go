package collrun

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// StoreConfig wires a StoreBackend.
type StoreConfig struct {
	Store  *store.Store
	Sender collexec.FlowSender
	Scope  collexec.ScopeChecker
	// OwnPorts / OwnIPs are the tool's own listeners; sends to them are refused.
	OwnPorts []int
	OwnIPs   []net.IP
	// PinnedHashes are script hashes trusted for this process only (CLI
	// --trust-hash). They are never written to the store.
	PinnedHashes []string
	Registry     *redact.Registry
	Clock        func() time.Time
	// Source/AI describe the caller for script sends.
	Source collexec.Source
	AI     bool
}

// StoreBackend is the Backend over a project store: it reads variables from
// ix_* tables, runs the shared pipeline with the in-process pm.* sandbox and
// writes kept variables back as local current values. The headless CLI uses
// it directly; the control layer may supply its own Backend instead.
type StoreBackend struct {
	cfg  StoreConfig
	jars *collexec.Jars
	reg  *redact.Registry
	pins map[string]bool
	mu   sync.Mutex // serialises variable commits
}

// NewStoreBackend builds a StoreBackend.
func NewStoreBackend(cfg StoreConfig) *StoreBackend {
	reg := cfg.Registry
	if reg == nil {
		reg = redact.NewRegistry()
	}
	pins := make(map[string]bool, len(cfg.PinnedHashes))
	for _, h := range cfg.PinnedHashes {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			pins[h] = true
		}
	}
	return &StoreBackend{cfg: cfg, jars: &collexec.Jars{}, reg: reg, pins: pins}
}

var _ Backend = (*StoreBackend)(nil)

// Load implements Backend.
func (b *StoreBackend) Load(collectionUID string) (store.Collection, []store.Item, error) {
	c, err := b.cfg.Store.GetCollection(collectionUID)
	if err != nil {
		return store.Collection{}, nil, err
	}
	items, err := b.cfg.Store.ListItems(c.UID)
	if err != nil {
		return store.Collection{}, nil, err
	}
	return *c, items, nil
}

// EnvName implements Backend.
func (b *StoreBackend) EnvName(envUID string) string {
	if envUID == "" {
		return ""
	}
	if e, err := b.cfg.Store.GetEnvironment(envUID); err == nil {
		return e.Name
	}
	return ""
}

// EnvPin implements Backend.
func (b *StoreBackend) EnvPin(envUID string) string {
	if envUID == "" {
		return ""
	}
	if e, err := b.cfg.Store.GetEnvironment(envUID); err == nil {
		return e.BaseTargetPin
	}
	return ""
}

// Scrub implements Backend: registered secrets first, then credential-shaped
// text. This is the single scrub function every run output passes through.
func (b *StoreBackend) Scrub(s string) string { return redact.Text(b.reg.Mask(s)) }

// Trusted implements Backend: a stored trust row or a process-local pin.
func (b *StoreBackend) Trusted(collectionUID, hash string) bool {
	if b.pins[strings.ToLower(hash)] {
		return true
	}
	ok, err := b.cfg.Store.IsScriptTrusted(collectionUID, hash)
	return err == nil && ok
}

// IsScriptTrusted lets the StoreBackend act as the pipeline's TrustChecker.
func (b *StoreBackend) IsScriptTrusted(collectionUID, hash string) (bool, error) {
	return b.Trusted(collectionUID, hash), nil
}

// Step implements Backend: one pipeline per step (cheap) sharing the cookie
// jars and masking registry, with the sandbox executor bound to this step.
func (b *StoreBackend) Step(ctx context.Context, in collexec.StepInput, meta StepMeta) (*collexec.StepResult, error) {
	exec := &PMExecutor{
		Coll: in.Chain.Collection, Source: in.Source, AI: in.AI, EnvUID: in.EnvUID, Layers: in.Layers,
		Iter: in.Iteration, Count: meta.IterationCount, Reg: b.reg, Flows: b.cfg.Store, Clock: b.cfg.Clock, Scope: b.cfg.Scope,
	}
	p := collexec.NewPipeline(collexec.Pipeline{
		Sender: b.cfg.Sender, Exec: exec, Scope: b.cfg.Scope, Trust: b, Flows: b.cfg.Store, Bodies: b.cfg.Store,
		Jars: b.jars, Registry: b.reg, OwnPorts: b.cfg.OwnPorts, OwnIPs: b.cfg.OwnIPs, Clock: b.cfg.Clock,
	})
	exec.Pipe = p
	return p.Step(ctx, in)
}

// ---- variables -----------------------------------------------------------------

func (b *StoreBackend) effectiveVars(kind, uid string) (map[string]varstore.Var, error) {
	decl, err := b.cfg.Store.ListVariables(kind, uid)
	if err != nil {
		return nil, err
	}
	cur, err := b.cfg.Store.ListCurrentValues(kind, uid)
	if err != nil {
		return nil, err
	}
	cm := make(map[string]string, len(cur))
	for _, v := range cur {
		cm[v.Key] = v.Value
	}
	out := make(map[string]varstore.Var, len(decl))
	for _, d := range decl {
		if !d.Enabled {
			continue
		}
		v := varstore.Var{Value: d.InitialValue, Secret: d.Type == store.VarTypeSecret}
		if cv, ok := cm[d.Key]; ok {
			v.Value = cv
		}
		out[d.Key] = v
	}
	return out, nil
}

// Layers implements Backend: globals, collection, folders (inner to outer),
// the chosen environment, and the request's own variables as the local map.
func (b *StoreBackend) Layers(chain collexec.Chain, envUID string) (layers []varstore.Layer, local map[string]string, err error) {
	st := b.cfg.Store
	add := func(scope varstore.Scope, name, kind, uid string) error {
		vs, err := b.effectiveVars(kind, uid)
		if err != nil {
			return err
		}
		layers = append(layers, varstore.Layer{Scope: scope, Name: name, Vars: vs})
		return nil
	}
	envs, err := st.ListEnvironments()
	if err != nil {
		return nil, nil, err
	}
	for _, e := range envs {
		if e.Kind == "globals" && (e.CollectionUID == "" || e.CollectionUID == chain.Collection.UID) {
			if err := add(varstore.ScopeGlobal, e.Name, store.VarOwnerEnvironment, e.UID); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := add(varstore.ScopeCollection, chain.Collection.Name, store.VarOwnerCollection, chain.Collection.UID); err != nil {
		return nil, nil, err
	}
	for i := len(chain.Folders) - 1; i >= 0; i-- {
		f := chain.Folders[i]
		if err := add(varstore.ScopeFolder, f.Name, store.VarOwnerFolder, f.UID); err != nil {
			return nil, nil, err
		}
	}
	if envUID != "" {
		e, err := st.GetEnvironment(envUID)
		if err != nil {
			return nil, nil, err
		}
		if e.Kind != "env" || (e.CollectionUID != "" && e.CollectionUID != chain.Collection.UID) {
			return nil, nil, errors.New("environment does not belong to this collection")
		}
		if err := add(varstore.ScopeEnvironment, e.Name, store.VarOwnerEnvironment, e.UID); err != nil {
			return nil, nil, err
		}
	}
	rv, err := b.effectiveVars(store.VarOwnerRequest, chain.Item.UID)
	if err != nil {
		return nil, nil, err
	}
	if len(rv) > 0 {
		local = make(map[string]string, len(rv))
		for k, v := range rv {
			local[k] = v.Value
		}
	}
	return layers, local, nil
}

// Commit implements Backend: script writes become local current values;
// initial values are never touched. Writes with no owner (no environment
// selected) are skipped and reported.
func (b *StoreBackend) Commit(coll store.Collection, envUID string, changes []collexec.VarChange) (skipped []string) {
	for _, ch := range changes {
		var kind, uid string
		switch ch.Scope {
		case collexec.VarEnvironment:
			kind, uid = store.VarOwnerEnvironment, envUID
		case collexec.VarCollection:
			kind, uid = store.VarOwnerCollection, coll.UID
		case collexec.VarGlobals:
			kind, uid = store.VarOwnerEnvironment, b.globalsUID(coll.UID)
		default:
			continue
		}
		if uid == "" || ch.Unset {
			skipped = append(skipped, ch.Scope+"."+ch.Key)
			continue
		}
		if err := b.setCurrent(kind, uid, ch.Key, ch.Value, ch.Secret); err != nil {
			skipped = append(skipped, ch.Scope+"."+ch.Key)
		}
	}
	return skipped
}

func (b *StoreBackend) setCurrent(kind, uid, key, value string, secret bool) error {
	if strings.TrimSpace(key) == "" || len(key) > 256 {
		return store.ErrCollInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	decl, err := b.cfg.Store.ListVariables(kind, uid)
	if err != nil {
		return err
	}
	found, isSecret := false, false
	for _, d := range decl {
		if d.Key == key {
			found, isSecret = true, d.Type == store.VarTypeSecret
		}
	}
	if !found {
		typ := store.VarTypeDefault
		if secret {
			typ, isSecret = store.VarTypeSecret, true
		}
		decl = append(decl, store.Variable{OwnerKind: kind, OwnerUID: uid, Key: key, Type: typ, Enabled: true})
		if err := b.cfg.Store.SetVariables(kind, uid, decl); err != nil {
			return err
		}
	}
	if isSecret {
		b.reg.Add(value)
	}
	return b.cfg.Store.SetCurrentValue(kind, uid, key, value, "script")
}

// globalsUID finds (or creates) the globals holder for a collection.
func (b *StoreBackend) globalsUID(collUID string) string {
	envs, err := b.cfg.Store.ListEnvironments()
	if err != nil {
		return ""
	}
	for _, e := range envs {
		if e.Kind == "globals" && (e.CollectionUID == "" || e.CollectionUID == collUID) {
			return e.UID
		}
	}
	e, err := b.cfg.Store.CreateEnvironment(store.Environment{Name: "Globals", Kind: "globals"})
	if err != nil {
		return ""
	}
	return e.UID
}
