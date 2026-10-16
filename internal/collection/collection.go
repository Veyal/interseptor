// Package collection is the domain layer over the collection tables in
// internal/store: ids, ordering, script-trust hashing and the Repo contract
// that other packages (control, collexec, collrun, MCP) depend on instead of
// *store.Store, so there is no import cycle and tests can fake it.
package collection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Re-exported model types: store owns the persistence shapes.
type (
	Collection  = store.Collection
	Item        = store.Item
	Environment = store.Environment
	Variable    = store.Variable
	Change      = store.CollChange
)

// Defaults decided for v1 (see the Postman-replacement plan, section 13).
const (
	DefaultScopePolicy       = store.ScopePolicyBlock // runner/CLI/scripts/MCP
	InteractiveScopePolicy   = store.ScopePolicyWarn  // single interactive sends
	DefaultRunnerPersist     = "ask"
	DefaultCLIPersist        = "discard"
	DefaultUnresolvedPolicy  = "block"
	DefaultSkipSessionHeader = true // collection items skip global session headers
)

// Repo is the persistence contract satisfied by *store.Store.
type Repo interface {
	CreateCollection(store.Collection) (*store.Collection, error)
	GetCollection(uid string) (*store.Collection, error)
	ListCollections() ([]store.Collection, error)
	UpdateCollection(store.Collection) (*store.Collection, error)
	DeleteCollection(uid string) error
	CreateItem(store.Item) (*store.Item, error)
	GetItem(uid string) (*store.Item, error)
	ListItems(collectionUID string) ([]store.Item, error)
	UpdateItem(store.Item, store.CollChange) (*store.Item, error)
	DeleteItem(uid string) error
	ListItemRevisions(itemUID string) ([]store.ItemRevision, error)
	CreateEnvironment(store.Environment) (*store.Environment, error)
	ListEnvironments() ([]store.Environment, error)
	SetVariables(ownerKind, ownerUID string, vars []store.Variable) error
	ListVariables(ownerKind, ownerUID string) ([]store.Variable, error)
	IsScriptTrusted(collectionUID, scriptHash string) (bool, error)
	ExportCollectionsBundle(store.ScrubOptions) (store.CollectionsBundle, error)
}

var _ Repo = (*store.Store)(nil)

// NewID returns a new ULID for collections, items and environments.
func NewID() string { return store.NewUID() }

// RankBetween returns an order key strictly between a and b ("" = open end).
func RankBetween(a, b string) string { return store.RankBetween(a, b) }

// Node is an item with its ordered children, for tree consumers.
type Node struct {
	store.Item
	Children []*Node `json:"children,omitempty"`
}

// BuildTree arranges a flat item list (any order) into ordered roots.
// Items whose parent is missing are returned as roots rather than dropped.
func BuildTree(items []store.Item) []*Node {
	nodes := make(map[string]*Node, len(items))
	for _, it := range items {
		nodes[it.UID] = &Node{Item: it}
	}
	var roots []*Node
	for _, it := range items {
		n := nodes[it.UID]
		if p, ok := nodes[it.ParentUID]; ok && it.ParentUID != "" && p != n {
			p.Children = append(p.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	var sortRec func([]*Node)
	sortRec = func(ns []*Node) {
		sort.SliceStable(ns, func(i, j int) bool {
			if ns[i].Rank != ns[j].Rank {
				return ns[i].Rank < ns[j].Rank
			}
			return ns[i].UID < ns[j].UID
		})
		for _, n := range ns {
			sortRec(n.Children)
		}
	}
	sortRec(roots)
	return roots
}

// ScriptHash is the identity used by script trust: sha256 over the exact
// source, the sorted library list and the sorted capability set, so any edit
// to code, libs or capabilities invalidates trust.
func ScriptHash(source string, libs, caps []string) string {
	h := sha256.New()
	write := func(tag string, parts []string) {
		h.Write([]byte(tag))
		for _, p := range parts {
			h.Write([]byte{0})
			h.Write([]byte(p))
		}
		h.Write([]byte{1})
	}
	write("src", []string{source})
	write("libs", sortedCopy(libs))
	write("caps", sortedCopy(caps))
	return hex.EncodeToString(h.Sum(nil))
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

type event struct {
	Listen string `json:"listen"`
	Script struct {
		Exec json.RawMessage `json:"exec"`
	} `json:"script"`
}

// EventSources extracts "<listen>\n<source>" for every script in an
// events_json column, accepting exec as a line array or a single string.
func EventSources(events json.RawMessage) []string {
	var evs []event
	if len(events) == 0 || json.Unmarshal(events, &evs) != nil {
		return nil
	}
	var out []string
	for _, e := range evs {
		var lines []string
		if json.Unmarshal(e.Script.Exec, &lines) != nil {
			var one string
			if json.Unmarshal(e.Script.Exec, &one) != nil {
				continue
			}
			lines = []string{one}
		}
		out = append(out, e.Listen+"\n"+strings.Join(lines, "\n"))
	}
	return out
}

// UntrustedScripts returns the hashes of scripts in events that are not
// trusted for the collection. Scripts from imports/merges/vault restores have
// no trust row, so they are all returned (quarantined). capabilities is the
// collection's cap set bound into each hash.
func UntrustedScripts(r Repo, collectionUID string, events json.RawMessage, caps []string) ([]string, error) {
	var out []string
	for _, src := range EventSources(events) {
		hash := ScriptHash(src, nil, caps)
		ok, err := r.IsScriptTrusted(collectionUID, hash)
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, hash)
		}
	}
	return out, nil
}
