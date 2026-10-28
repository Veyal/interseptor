package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const authzIdentitiesSetting = "authz.identities"

const (
	authzModeMerge   = "merge"
	authzModeReplace = "replace"
)

// mutateIdentities runs fn over the saved identity list under authzMu so that
// concurrent writers (parallel agents, UI) cannot lose each other's updates.
func (h *authzAPI) mutateIdentities(fn func([]identity) ([]identity, error)) ([]identity, error) {
	h.authzMu.Lock()
	defer h.authzMu.Unlock()
	cur, err := h.authzIdentitiesResult()
	if err != nil {
		return nil, err
	}
	next, err := fn(cur)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if err := h.st.SetSetting(authzIdentitiesSetting, string(b)); err != nil {
		return nil, err
	}
	return next, nil
}

// clearServerOwned drops the fields only the server writes (first-create stamp,
// provenance, health), so a caller cannot forge who created an identity or
// that its session was checked.
func clearServerOwned(id identity) identity {
	id.CreatedAt, id.CreatedBy, id.Source, id.LastCheck = "", "", nil, nil
	return id
}

// stampIdentity records write time, owner and first-create metadata on an
// identity. createdAt/createdBy are only filled when still empty; an upsert
// over an existing identity carries the originals across (see
// carryIdentityHistory), so they are written exactly once.
func stampIdentity(id identity, actor string, now time.Time) identity {
	id.Name = strings.TrimSpace(id.Name)
	id.UpdatedAt = now.UTC().Format(time.RFC3339)
	if actor != "" {
		id.Owner = actor
	}
	if id.CreatedAt == "" {
		id.CreatedAt = id.UpdatedAt
	}
	if id.CreatedBy == "" {
		id.CreatedBy = id.Owner
	}
	return id
}

// carryIdentityHistory preserves the metadata an upsert must not destroy: the
// first-create stamp always, and the workspace fields a caller that only sent
// {name, headers} never mentioned. PATCH edits in place and can clear them.
func carryIdentityHistory(prev, next identity) identity {
	if prev.CreatedAt != "" {
		next.CreatedAt = prev.CreatedAt
	}
	if prev.CreatedBy != "" {
		next.CreatedBy = prev.CreatedBy
	}
	if next.Role == "" {
		next.Role = prev.Role
	}
	if next.Notes == "" {
		next.Notes = prev.Notes
	}
	if next.Source == nil {
		next.Source = prev.Source
	}
	if next.LastCheck == nil {
		next.LastCheck = prev.LastCheck
	}
	// The legacy modal sends {name, headers, broken} only: a still-broken
	// identity keeps its reason. Un-breaking drops it with the flag.
	if next.Broken && prev.Broken && next.BrokenNote == "" {
		next.BrokenNote = prev.BrokenNote
	}
	return next
}

// identityUnchanged reports whether an upsert would leave the identity's
// user-visible content as it was, so a legacy Run that re-saves the whole
// list does not restamp or log identities nobody edited.
func identityUnchanged(prev, next identity) bool {
	return prev.Name == next.Name && prev.Headers == next.Headers && prev.Broken == next.Broken &&
		prev.BrokenNote == next.BrokenNote && prev.Role == next.Role && prev.Notes == next.Notes
}

// upsertIdentity replaces the identity with the same name, or appends it.
func upsertIdentity(list []identity, id identity) []identity {
	for i := range list {
		if list[i].Name == id.Name {
			list[i] = carryIdentityHistory(list[i], id)
			return list
		}
	}
	return append(list, id)
}

// findIdentity returns a copy of the identity named name.
func findIdentity(list []identity, name string) (identity, bool) {
	for _, id := range list {
		if id.Name == name {
			return id, true
		}
	}
	return identity{}, false
}

// identityWriteEvent is the event kind for one upsert: a first create, a
// broken-marking, or a plain update.
func identityWriteEvent(prev identity, existed bool, next identity) string {
	switch {
	case !existed:
		return identityEventCreated
	case next.Broken && !prev.Broken:
		return identityEventMarkedBroken
	default:
		return identityEventUpdated
	}
}

// setAuthz saves identities. Default mode is merge-by-name so parallel callers
// never destroy identities they did not write; mode "replace" restores the
// legacy whole-list overwrite.
func (h *authzAPI) setAuthz(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Identities []identity `json:"identities"`
		Mode       string     `json:"mode"`
		Owner      string     `json:"owner"`
	}
	if !decodeLimitedJSON(w, r, maxAuthzConfigRequestBytes, &in) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = authzModeMerge
	}
	if mode != authzModeMerge && mode != authzModeReplace {
		httpErr(w, http.StatusBadRequest, "mode must be merge or replace")
		return
	}
	now := time.Now()
	actor := identityActor(r, in.Owner)
	sourceKind := identityActorSourceKind(r)
	type written struct {
		name, event string
	}
	var log []written
	var removed []string
	out, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		log, removed = nil, nil
		// Replace drops identities missing from the list, but one that is still
		// listed keeps its history: the legacy modal sends only {name, headers}.
		before := cur
		if mode == authzModeReplace {
			cur = nil
		}
		seen := map[string]bool{}
		for _, id := range in.Identities {
			name := strings.TrimSpace(id.Name)
			// A name repeated in this payload updates what the earlier entry
			// wrote instead of logging a second create.
			src := before
			if seen[name] {
				src = cur
			}
			seen[name] = true
			prev, existed := findIdentity(src, name)
			stamped := stampIdentity(clearServerOwned(id), actor, now)
			if existed {
				stamped = carryIdentityHistory(prev, stamped)
				if identityUnchanged(prev, stamped) {
					// Legacy Run re-saves every row: keep the untouched one
					// as it was (owner, updatedAt) and log nothing.
					cur = upsertIdentity(cur, prev)
					continue
				}
			} else {
				stamped.Source = &identitySource{Kind: sourceKind}
			}
			log = append(log, written{stamped.Name, identityWriteEvent(prev, existed, stamped)})
			cur = upsertIdentity(cur, stamped)
		}
		if mode == authzModeReplace {
			for _, id := range before {
				if _, kept := findIdentity(cur, id.Name); !kept {
					removed = append(removed, id.Name)
				}
			}
		}
		return cur, nil
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	for _, name := range removed {
		h.dropIdentityEvents(name)
	}
	for _, wr := range log {
		h.appendIdentityEvent(wr.name, wr.event, actor, "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out, "mode": mode})
	for _, wr := range log {
		h.broadcastAuthzIdentities(wr.name)
	}
	for _, name := range removed {
		h.broadcastAuthzIdentities(name)
	}
}

// addAuthzIdentity upserts exactly one identity by name.
func (h *authzAPI) addAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	var id identity
	if !decodeLimitedJSON(w, r, maxAuthzConfigRequestBytes, &id) {
		return
	}
	if strings.TrimSpace(id.Name) == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	actor := identityActor(r, id.Owner)
	sourceKind := identityActorSourceKind(r)
	var event, name string
	out, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		prev, existed := findIdentity(cur, strings.TrimSpace(id.Name))
		stamped := stampIdentity(clearServerOwned(id), actor, time.Now())
		if existed {
			stamped = carryIdentityHistory(prev, stamped)
		} else {
			stamped.Source = &identitySource{Kind: sourceKind}
		}
		event, name = identityWriteEvent(prev, existed, stamped), stamped.Name
		return upsertIdentity(cur, stamped), nil
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	h.appendIdentityEvent(name, event, actor, "")
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
	h.broadcastAuthzIdentities(name)
}

// removeAuthzIdentity deletes one identity by name; other identities are untouched.
func (h *authzAPI) removeAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	// PathValue is already unescaped; unescaping again breaks names with '%'.
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	found := false
	out, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		kept := cur[:0:0]
		for _, id := range cur {
			if id.Name == name {
				found = true
				continue
			}
			kept = append(kept, id)
		}
		if !found {
			return cur, nil
		}
		return kept, nil
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !found {
		httpErr(w, http.StatusNotFound, "identity not found")
		return
	}
	h.dropIdentityEvents(name)
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
	h.broadcastAuthzIdentities(name)
}
