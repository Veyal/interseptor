package control

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
	"github.com/Veyal/interseptor/internal/store"
)

// Capability names a collection can grant its scripts. net.outOfScope,
// findings.write, identity.read and oob exist in the plan but are not
// grantable in v1: nothing here may widen what a script can reach.
const (
	CapVarsRead     = "vars.read"
	CapVarsWrite    = "vars.write"
	CapCookiesRead  = "cookies.read"
	CapCookiesWrite = "cookies.write"
	CapNetSend      = "net.send"
	CapSecretsRead  = "secrets.read"
)

var grantableCaps = []string{CapVarsRead, CapVarsWrite, CapCookiesRead, CapCookiesWrite, CapNetSend, CapSecretsRead}

// capNames parses a collection's caps column the way collexec binds it into
// script hashes (array of names or object of name->bool).
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

// sandboxCaps maps granted capability names to the sandbox's switches. An
// empty set grants nothing (default deny).
func sandboxCaps(names []string) scriptctx.Caps {
	var c scriptctx.Caps
	for _, n := range names {
		switch n {
		case CapVarsRead:
			c.VarsRead = true
		case CapVarsWrite:
			c.VarsWrite = true
		case CapCookiesRead:
			c.CookiesRead = true
		case CapCookiesWrite:
			c.CookiesWrite = true
		case CapNetSend:
			c.NetSend = true
		case CapSecretsRead:
			c.SecretsRead = true
		}
	}
	return c
}

// scriptEntry is one distinct script of a collection.
type scriptEntry struct {
	Hash    string   `json:"hash"`
	Listen  string   `json:"listen"`
	Owners  []string `json:"owners"` // item uids ("" entry = the collection itself)
	Trusted bool     `json:"trusted"`
	Lines   int      `json:"lines"`
	APIs    []string `json:"apis,omitempty"`
	Modules []string `json:"modules,omitempty"`
	Hosts   []string `json:"hosts,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	Status  string   `json:"status"` // supported | partial | unsupported
	Source  string   `json:"source,omitempty"`
	src     string
}

func splitSource(es string) (listen, src string) {
	for i := 0; i < len(es); i++ {
		if es[i] == '\n' {
			return es[:i], es[i+1:]
		}
	}
	return es, ""
}

// scriptsOf lists every distinct script in a collection with its trust
// state under the given capability set.
func (c *collectionsAPI) scriptsOf(co store.Collection, items []store.Item, caps []string) ([]scriptEntry, error) {
	by := map[string]*scriptEntry{}
	var order []string
	collect := func(owner string, events json.RawMessage) error {
		for _, es := range collection.EventSources(events) {
			h := collection.ScriptHash(es, nil, caps)
			e, ok := by[h]
			if !ok {
				listen, src := splitSource(es)
				trusted, err := c.h.st.IsScriptTrusted(co.UID, h)
				if err != nil {
					return err
				}
				e = &scriptEntry{Hash: h, Listen: listen, Trusted: trusted, src: src}
				rep := pmsandbox.Analyze(src)
				e.Lines, e.APIs, e.Modules = rep.Lines, rep.APIs, rep.Modules
				for _, hr := range rep.Hosts {
					e.Hosts = append(e.Hosts, hr.Host)
				}
				for _, f := range rep.Flags {
					e.Flags = append(e.Flags, f.Name)
				}
				switch {
				case rep.HasUnsupported():
					e.Status = "unsupported"
				case len(rep.Unsupported) > 0:
					e.Status = "partial"
				default:
					e.Status = "supported"
				}
				by[h] = e
				order = append(order, h)
			}
			e.Owners = append(e.Owners, owner)
		}
		return nil
	}
	if err := collect("", co.Events); err != nil {
		return nil, err
	}
	for _, it := range items {
		if err := collect(it.UID, it.Events); err != nil {
			return nil, err
		}
	}
	out := make([]scriptEntry, 0, len(order))
	for _, h := range order {
		out = append(out, *by[h])
	}
	return out, nil
}

type scriptsView struct {
	Capabilities []string      `json:"capabilities"`
	Grantable    []string      `json:"grantable"`
	Scripts      []scriptEntry `json:"scripts"`
	Untrusted    int           `json:"untrusted"`
}

func (c *collectionsAPI) scriptsView(uid string, withSource bool) (*scriptsView, error) {
	co, err := c.h.st.GetCollection(uid)
	if err != nil {
		return nil, err
	}
	items, err := c.h.st.ListItems(uid)
	if err != nil {
		return nil, err
	}
	caps := capNames(co.Caps)
	list, err := c.scriptsOf(*co, items, caps)
	if err != nil {
		return nil, err
	}
	v := &scriptsView{Capabilities: append([]string{}, caps...), Grantable: grantableCaps, Scripts: list}
	for i := range v.Scripts {
		if !v.Scripts[i].Trusted {
			v.Untrusted++
		}
		if withSource {
			v.Scripts[i].Source = v.Scripts[i].src
		}
	}
	return v, nil
}

// listScripts is the review sheet source (hash, source, analysis) for the UI
// and the read-only approval status for the AI channel (no source text).
func (c *collectionsAPI) listScripts(w http.ResponseWriter, r *http.Request) {
	v, err := c.scriptsView(r.PathValue("uid"), !isAISource(r))
	if err != nil {
		collErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type trustInput struct {
	Confirm      bool      `json:"confirm"`
	All          bool      `json:"all"`
	Hashes       []string  `json:"hashes"`
	Capabilities *[]string `json:"capabilities"`
}

func validCaps(in []string) bool {
	for _, n := range in {
		if !slices.Contains(grantableCaps, n) {
			return false
		}
	}
	return true
}

// approve trusts the selected scripts (and optionally sets the capability set,
// which re-binds every hash). It is reachable only from an interactive UI
// session: requireUISession rejects the AI/MCP channel and API keys.
func (c *collectionsAPI) approve(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	var in trustInput
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if !in.Confirm {
		httpErr(w, http.StatusBadRequest, "confirm must be true: trusting a script lets it run with this collection's capabilities")
		return
	}
	if !in.All && len(in.Hashes) == 0 {
		httpErr(w, http.StatusBadRequest, "pass all:true or the script hashes to trust")
		return
	}
	if in.Capabilities != nil && !validCaps(*in.Capabilities) {
		httpErr(w, http.StatusBadRequest, "capabilities may only be: "+joinStrings(grantableCaps))
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	co, err := c.h.st.GetCollection(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	items, err := c.h.st.ListItems(co.UID)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	before, err := c.scriptsOf(*co, items, capNames(co.Caps))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	selected := map[string]bool{}
	if in.All {
		for _, s := range before {
			selected[s.Hash] = true
		}
	} else {
		known := map[string]bool{}
		for _, s := range before {
			known[s.Hash] = true
		}
		for _, h := range in.Hashes {
			if !known[h] {
				httpErr(w, http.StatusBadRequest, "unknown script hash (the collection changed; reload the review sheet)")
				return
			}
			selected[h] = true
		}
	}
	finalCaps := capNames(co.Caps)
	if in.Capabilities != nil {
		finalCaps = append([]string{}, *in.Capabilities...)
		sort.Strings(finalCaps)
		next := *co
		raw, _ := json.Marshal(finalCaps)
		next.Caps, next.Rev = raw, 0
		if co, err = c.h.st.UpdateCollection(next); err != nil {
			collErr(w, err)
			return
		}
	}
	// Re-derive hashes under the final capability set and trust only the
	// scripts the caller selected (matched by their pre-change hash).
	after, err := c.scriptsOf(*co, items, finalCaps)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	trusted := 0
	for i, s := range after {
		if !selected[before[i].Hash] {
			continue
		}
		if err := c.h.st.TrustScript(co.UID, s.Hash); err != nil {
			httpInternalErr(w, err)
			return
		}
		trusted++
	}
	v, err := c.scriptsView(co.UID, false)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trusted": trusted, "status": v})
}

// revoke removes trust for the given hashes or for the whole collection. It
// only ever reduces what runs, but it is still session-gated so an agent
// cannot silently disable a human's test scripts.
func (c *collectionsAPI) revoke(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	var in trustInput
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if !in.All && len(in.Hashes) == 0 {
		httpErr(w, http.StatusBadRequest, "pass all:true or the script hashes to revoke")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.scriptsView(r.PathValue("uid"), false)
	if err != nil {
		collErr(w, err)
		return
	}
	want := map[string]bool{}
	for _, h := range in.Hashes {
		want[h] = true
	}
	revoked := 0
	for _, s := range v.Scripts {
		if !s.Trusted || (!in.All && !want[s.Hash]) {
			continue
		}
		if err := c.h.st.RevokeScriptTrust(r.PathValue("uid"), s.Hash); err != nil {
			httpInternalErr(w, err)
			return
		}
		revoked++
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

// autoTrustOwnEdit trusts scripts a human just wrote in the UI: a hash that is
// in the new events but was not in the old ones. Unchanged scripts keep their
// current state, so saving a rename never approves an imported script. The
// AI channel, API keys and requests without the UI marker never auto-trust.
func (c *collectionsAPI) autoTrustOwnEdit(r *http.Request, collUID string, oldEvents, newEvents []json.RawMessage, caps []string) {
	if requireUISession(r) != nil {
		return
	}
	old := map[string]bool{}
	for _, ev := range oldEvents {
		for _, es := range collection.EventSources(ev) {
			old[collection.ScriptHash(es, nil, caps)] = true
		}
	}
	for _, ev := range newEvents {
		for _, es := range collection.EventSources(ev) {
			h := collection.ScriptHash(es, nil, caps)
			if !old[h] {
				_ = c.h.st.TrustScript(collUID, h) // best effort: failure leaves it quarantined
			}
		}
	}
}

func joinStrings(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
