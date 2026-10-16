package collrun

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/store"
)

type scriptEvent struct {
	Listen   string `json:"listen"`
	Disabled bool   `json:"disabled"`
	Script   struct {
		Exec json.RawMessage `json:"exec"`
	} `json:"script"`
}

// ScriptRef is one runnable script in a collection.
type ScriptRef struct {
	Owner   string `json:"owner"` // collection | folder | request
	Name    string `json:"name,omitempty"`
	ItemUID string `json:"itemUid,omitempty"`
	Listen  string `json:"listen"`
	Source  string `json:"-"`
	Hash    string `json:"hash"`
}

// capNames parses a collection's caps column the way the pipeline binds it
// into script hashes (array of names or object of name->bool).
func capNames(raw json.RawMessage) []string {
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var m map[string]bool
	if json.Unmarshal(raw, &m) == nil {
		var out []string
		for k, v := range m {
			if v {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// eventRefs lists the enabled, non-empty scripts of an events column. The
// hash input ("<listen>\n<source>", libs none, caps bound) is exactly what the
// pipeline's trust gate and collection.UntrustedScripts use.
func eventRefs(owner, name, itemUID string, events json.RawMessage, caps []string) []ScriptRef {
	var evs []scriptEvent
	if len(events) == 0 || json.Unmarshal(events, &evs) != nil {
		return nil
	}
	var out []ScriptRef
	for _, e := range evs {
		if e.Disabled || (e.Listen != "prerequest" && e.Listen != "test") {
			continue
		}
		var lines []string
		if json.Unmarshal(e.Script.Exec, &lines) != nil {
			var one string
			if json.Unmarshal(e.Script.Exec, &one) != nil {
				continue
			}
			lines = []string{one}
		}
		src := strings.Join(lines, "\n")
		if strings.TrimSpace(src) == "" {
			continue
		}
		out = append(out, ScriptRef{Owner: owner, Name: name, ItemUID: itemUID, Listen: e.Listen, Source: src,
			Hash: collection.ScriptHash(e.Listen+"\n"+src, nil, caps)})
	}
	return out
}

// CollectionScripts lists every runnable script of a collection (collection,
// folders and requests), in tree order.
func CollectionScripts(coll store.Collection, items []store.Item) []ScriptRef {
	caps := capNames(coll.Caps)
	out := eventRefs("collection", coll.Name, "", coll.Events, caps)
	var walk func(ns []*collection.Node)
	walk = func(ns []*collection.Node) {
		for _, n := range ns {
			owner := "request"
			if n.Kind == "folder" {
				owner = "folder"
			}
			out = append(out, eventRefs(owner, n.Name, n.UID, n.Events, caps)...)
			walk(n.Children)
		}
	}
	walk(collection.BuildTree(items))
	return out
}

// planScripts lists the scripts that would run for the plan: the collection's,
// the folders on each planned request's path and the request's own.
func planScripts(coll store.Collection, items []store.Item, plan []store.Item) []ScriptRef {
	caps := capNames(coll.Caps)
	by := make(map[string]store.Item, len(items))
	for _, it := range items {
		by[it.UID] = it
	}
	seen := map[string]bool{}
	var out []ScriptRef
	add := func(refs []ScriptRef) {
		for _, r := range refs {
			k := r.Owner + "\x00" + r.ItemUID + "\x00" + r.Hash
			if !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
	}
	add(eventRefs("collection", coll.Name, "", coll.Events, caps))
	for _, it := range plan {
		var folders []store.Item
		guard := map[string]bool{it.UID: true}
		for p := it.ParentUID; p != ""; {
			f, ok := by[p]
			if !ok || guard[p] {
				break
			}
			guard[p] = true
			folders = append([]store.Item{f}, folders...)
			p = f.ParentUID
		}
		for _, f := range folders {
			add(eventRefs("folder", f.Name, f.UID, f.Events, caps))
		}
		add(eventRefs("request", it.Name, it.UID, it.Events, caps))
	}
	return out
}

// untrustedInPlan returns the plan's scripts the backend does not trust.
func (r *Runner) untrustedInPlan(coll store.Collection, items []store.Item, plan []store.Item) []QuarantinedScript {
	var out []QuarantinedScript
	seen := map[string]bool{}
	for _, s := range planScripts(coll, items, plan) {
		if seen[s.Hash] || r.Backend.Trusted(coll.UID, s.Hash) {
			continue
		}
		seen[s.Hash] = true
		out = append(out, QuarantinedScript{Owner: s.Owner, Name: s.Name, Listen: s.Listen, Hash: s.Hash})
	}
	return out
}
