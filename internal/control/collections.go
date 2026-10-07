package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
)

// Request-size budgets for the collection routes. They are vars so tests can
// shrink them instead of allocating multi-megabyte fixtures.
var (
	maxCollectionJSONBytes   int64 = 8 << 20  // collection/item/example bodies
	maxCollectionSmallBytes  int64 = 1 << 20  // variables, trust, send, run requests
	maxCollectionImportBytes int64 = 64 << 20 // import preview/commit (matches the parser cap)
)

const (
	maxRunItems       = 1000 // requests one run may execute
	maxRunSteps       = 5000 // loop guard for setNextRequest
	maxVariablesBatch = 5000
	maxExamplesPerReq = 100
)

// collectionsAPI serves collections, items, examples, environments,
// variables, import, send/run and script trust. It holds the cookie jars and
// the secret-masking registry shared by every send of the process.
type collectionsAPI struct {
	h    *Hub
	jars *collexec.Jars
	reg  *redact.Registry
	mu   sync.Mutex // serializes variable commits and trust changes

	beOnce sync.Once
	be     *collrun.StoreBackend
	mgr    *collrun.Manager // asynchronous runs (UI runner, MCP, REST)
	oauth  oauthPending     // authorization-code flows awaiting their callback
}

func newCollectionsAPI(h *Hub) *collectionsAPI {
	return &collectionsAPI{h: h, jars: &collexec.Jars{}, reg: redact.NewRegistry(), mgr: collrun.NewManager()}
}

// isAISource reports whether the request came through the MCP tool bus (or any
// caller that identifies itself as the AI channel).
func isAISource(r *http.Request) bool { return aiSourceFlag(r) != 0 }

// errNotUISession explains why trust-class routes refuse a caller.
var errNotUISession = errors.New("script trust and capability grants require an interactive UI session; AI, MCP, API-key and agent callers can never approve scripts")

// requireUISession allows only an interactive UI session: no AI/MCP source
// marker, no bearer API key (keys are for agents and CLIs), and the anti-CSRF
// marker the UI attaches to every mutation. It is the single gate for trust
// and capability changes.
func requireUISession(r *http.Request) error {
	if src := strings.TrimSpace(r.Header.Get("X-Interseptor-Source")); src != "" && !strings.EqualFold(src, "ui") {
		return errNotUISession
	}
	if bearerToken(r) != "" {
		return errNotUISession
	}
	if !csrfHeaderOK(r) {
		return errors.New("missing X-Interseptor-CSRF header")
	}
	return nil
}

// collErr maps store errors to HTTP statuses without leaking internals.
func collErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCollNotFound):
		httpErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrCollConflict):
		httpErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrCollInvalid):
		httpErr(w, http.StatusBadRequest, err.Error())
	default:
		httpInternalErr(w, err)
	}
}

func actorFor(r *http.Request) store.CollChange {
	if isAISource(r) {
		return store.CollChange{Actor: "ai", Source: "mcp"}
	}
	return store.CollChange{Actor: "user", Source: "ui"}
}

// scrubbedBundle returns the portable bundle with every secret blanked; it is
// the single source for anything shown to the AI channel.
func (c *collectionsAPI) scrubbedBundle() (store.CollectionsBundle, error) {
	return c.h.st.ExportCollectionsBundle(store.ScrubOptions{})
}

type collectionTree struct {
	Collection store.Collection `json:"collection"`
	Items      []store.Item     `json:"items"`
}

// loadTree reads a collection with its items. Callers on the AI channel get
// the scrubbed form.
func (c *collectionsAPI) loadTree(uid string, scrub bool) (*collectionTree, error) {
	if scrub {
		b, err := c.scrubbedBundle()
		if err != nil {
			return nil, err
		}
		t := &collectionTree{Items: []store.Item{}}
		found := false
		for _, co := range b.Collections {
			if co.UID == uid {
				t.Collection, found = co, true
			}
		}
		if !found {
			return nil, store.ErrCollNotFound
		}
		for _, it := range b.Items {
			if it.CollectionUID == uid {
				t.Items = append(t.Items, it)
			}
		}
		return t, nil
	}
	co, err := c.h.st.GetCollection(uid)
	if err != nil {
		return nil, err
	}
	items, err := c.h.st.ListItems(uid)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []store.Item{}
	}
	return &collectionTree{Collection: *co, Items: items}, nil
}

// ---- collections -------------------------------------------------------------

func (c *collectionsAPI) list(w http.ResponseWriter, r *http.Request) {
	var cs []store.Collection
	var err error
	if isAISource(r) {
		var b store.CollectionsBundle
		if b, err = c.scrubbedBundle(); err == nil {
			cs = b.Collections
		}
	} else {
		cs, err = c.h.st.ListCollections()
	}
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if cs == nil {
		cs = []store.Collection{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": cs})
}

// collectionInput is the writable surface of a collection. Capabilities and
// the scope policy are deliberately excluded for the AI channel, and caps are
// excluded for everyone: they change only through the trust route.
type collectionInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Auth        json.RawMessage `json:"auth"`
	Events      json.RawMessage `json:"events"`
	Settings    json.RawMessage `json:"settings"`
	ScopePolicy string          `json:"scopePolicy"`
	Rev         int64           `json:"rev"`
}

func (in collectionInput) apply(co *store.Collection) {
	co.Name, co.Description = in.Name, in.Description
	co.Auth, co.Events, co.Settings = in.Auth, in.Events, in.Settings
	if in.ScopePolicy != "" {
		co.ScopePolicy = in.ScopePolicy
	}
}

func (c *collectionsAPI) create(w http.ResponseWriter, r *http.Request) {
	var in collectionInput
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &in) {
		return
	}
	if isAISource(r) && in.ScopePolicy != "" && in.ScopePolicy != store.ScopePolicyBlock {
		httpErr(w, http.StatusForbidden, "the AI channel cannot loosen a collection's scope policy")
		return
	}
	co := store.Collection{ScopePolicy: store.ScopePolicyBlock}
	in.apply(&co)
	out, err := c.h.st.CreateCollection(co)
	if err != nil {
		collErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (c *collectionsAPI) get(w http.ResponseWriter, r *http.Request) {
	t, err := c.loadTree(r.PathValue("uid"), isAISource(r))
	if err != nil {
		collErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (c *collectionsAPI) update(w http.ResponseWriter, r *http.Request) {
	var in collectionInput
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &in) {
		return
	}
	cur, err := c.h.st.GetCollection(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	if isAISource(r) && in.ScopePolicy != "" && in.ScopePolicy != cur.ScopePolicy {
		httpErr(w, http.StatusForbidden, "the AI channel cannot change a collection's scope policy")
		return
	}
	next := *cur
	in.apply(&next)
	next.Rev = in.Rev
	out, err := c.h.st.UpdateCollection(next)
	if err != nil {
		collErr(w, err)
		return
	}
	c.autoTrustOwnEdit(r, out.UID, []json.RawMessage{cur.Events}, []json.RawMessage{out.Events}, capNames(out.Caps))
	writeJSON(w, http.StatusOK, out)
}

func (c *collectionsAPI) remove(w http.ResponseWriter, r *http.Request) {
	if err := c.h.st.DeleteCollection(r.PathValue("uid")); err != nil {
		collErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- items -------------------------------------------------------------------

func (c *collectionsAPI) createItem(w http.ResponseWriter, r *http.Request) {
	var it store.Item
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &it) {
		return
	}
	it.CollectionUID, it.UID, it.Rev = r.PathValue("uid"), "", 0
	out, err := c.h.st.CreateItem(it)
	if err != nil {
		collErr(w, err)
		return
	}
	co, cerr := c.h.st.GetCollection(out.CollectionUID)
	if cerr == nil {
		c.autoTrustOwnEdit(r, co.UID, nil, []json.RawMessage{out.Events}, capNames(co.Caps))
	}
	writeJSON(w, http.StatusCreated, out)
}

func (c *collectionsAPI) getItem(w http.ResponseWriter, r *http.Request) {
	it, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	if isAISource(r) {
		t, terr := c.loadTree(it.CollectionUID, true)
		if terr != nil {
			collErr(w, terr)
			return
		}
		for _, x := range t.Items {
			if x.UID == it.UID {
				writeJSON(w, http.StatusOK, x)
				return
			}
		}
		httpErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (c *collectionsAPI) updateItem(w http.ResponseWriter, r *http.Request) {
	var it store.Item
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &it) {
		return
	}
	cur, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	it.UID = cur.UID
	out, err := c.h.st.UpdateItem(it, actorFor(r))
	if err != nil {
		collErr(w, err)
		return
	}
	if co, cerr := c.h.st.GetCollection(out.CollectionUID); cerr == nil {
		c.autoTrustOwnEdit(r, co.UID, []json.RawMessage{cur.Events}, []json.RawMessage{out.Events}, capNames(co.Caps))
	}
	writeJSON(w, http.StatusOK, out)
}

func (c *collectionsAPI) removeItem(w http.ResponseWriter, r *http.Request) {
	if err := c.h.st.DeleteItem(r.PathValue("uid")); err != nil {
		collErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// duplicateItem copies a request (not a folder subtree) next to the original.
// A copy is a new item: its scripts are quarantined like any other new script
// unless the hash already has trust (identical source stays trusted).
func (c *collectionsAPI) duplicateItem(w http.ResponseWriter, r *http.Request) {
	cur, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	if cur.Kind == "folder" {
		httpErr(w, http.StatusBadRequest, "folders cannot be duplicated; duplicate its requests")
		return
	}
	cp := *cur
	cp.UID, cp.Rev, cp.Rank = "", 0, ""
	cp.Name = cur.Name + " copy"
	out, err := c.h.st.CreateItem(cp)
	if err != nil {
		collErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
