package control

import (
	"encoding/json"
	"net/http"
	"net/url"
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

// stampIdentity records write time and (when given) owner on an identity.
func stampIdentity(id identity, owner string, now time.Time) identity {
	id.Name = strings.TrimSpace(id.Name)
	id.UpdatedAt = now.UTC().Format(time.RFC3339)
	if owner != "" {
		id.Owner = owner
	}
	return id
}

// upsertIdentity replaces the identity with the same name, or appends it.
func upsertIdentity(list []identity, id identity) []identity {
	for i := range list {
		if list[i].Name == id.Name {
			list[i] = id
			return list
		}
	}
	return append(list, id)
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
	owner := strings.TrimSpace(in.Owner)
	out, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		if mode == authzModeReplace {
			cur = nil
		}
		for _, id := range in.Identities {
			cur = upsertIdentity(cur, stampIdentity(id, owner, now))
		}
		return cur, nil
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out, "mode": mode})
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
	out, err := h.mutateIdentities(func(cur []identity) ([]identity, error) {
		return upsertIdentity(cur, stampIdentity(id, "", time.Now())), nil
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
}

// removeAuthzIdentity deletes one identity by name; other identities are untouched.
func (h *authzAPI) removeAuthzIdentity(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(r.PathValue("name"))
	name = strings.TrimSpace(name)
	if err != nil || name == "" {
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
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
}
