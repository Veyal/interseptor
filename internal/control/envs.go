package control

import (
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

const secretMask = "[secret]"

// ---- environments ------------------------------------------------------------

// varView is one declared variable as shown to a caller. A secret's value is
// never returned: only whether a local current value exists.
type varView struct {
	Key          string `json:"key"`
	Type         string `json:"type"`
	InitialValue string `json:"initialValue"`
	Enabled      bool   `json:"enabled"`
	Current      string `json:"current,omitempty"`
	HasCurrent   bool   `json:"hasCurrent"`
}

// viewVars merges declared variables with their current values. Secret
// values are masked unless reveal is set (human sessions only).
func (c *collectionsAPI) viewVars(kind, uid string, reveal bool) ([]varView, error) {
	decl, err := c.h.st.ListVariables(kind, uid)
	if err != nil {
		return nil, err
	}
	cur, err := c.h.st.ListCurrentValues(kind, uid)
	if err != nil {
		return nil, err
	}
	cm := make(map[string]string, len(cur))
	for _, v := range cur {
		cm[v.Key] = v.Value
	}
	out := make([]varView, 0, len(decl))
	for _, d := range decl {
		v := varView{Key: d.Key, Type: d.Type, InitialValue: d.InitialValue, Enabled: d.Enabled}
		if cv, ok := cm[d.Key]; ok {
			v.HasCurrent = true
			if d.Type == store.VarTypeSecret && !reveal {
				v.Current = secretMask
			} else {
				v.Current = cv
			}
		}
		out = append(out, v)
	}
	return out, nil
}

type envView struct {
	store.Environment
	Variables []varView `json:"variables"`
}

func (c *collectionsAPI) envView(e store.Environment, reveal bool) (envView, error) {
	vs, err := c.viewVars(store.VarOwnerEnvironment, e.UID, reveal)
	return envView{Environment: e, Variables: vs}, err
}

func revealSecrets(r *http.Request) bool {
	return r.URL.Query().Get("reveal") == "1" && !isAISource(r) && requireUISession(r) == nil
}

func (c *collectionsAPI) listEnvs(w http.ResponseWriter, r *http.Request) {
	envs, err := c.h.st.ListEnvironments()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	reveal := revealSecrets(r)
	out := make([]envView, 0, len(envs))
	for _, e := range envs {
		v, err := c.envView(e, reveal)
		if err != nil {
			httpInternalErr(w, err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out})
}

type envInput struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	CollectionUID string            `json:"collectionUid"`
	BoundIdentity string            `json:"boundIdentity"`
	BaseTargetPin string            `json:"baseTargetPin"`
	Rev           int64             `json:"rev"`
	Variables     *[]store.Variable `json:"variables"`
}

func (c *collectionsAPI) createEnv(w http.ResponseWriter, r *http.Request) {
	var in envInput
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if in.Variables != nil && len(*in.Variables) > maxVariablesBatch {
		httpErr(w, http.StatusBadRequest, "too many variables")
		return
	}
	e, err := c.h.st.CreateEnvironment(store.Environment{Name: in.Name, Kind: in.Kind,
		CollectionUID: in.CollectionUID, BoundIdentity: in.BoundIdentity, BaseTargetPin: in.BaseTargetPin})
	if err != nil {
		collErr(w, err)
		return
	}
	if in.Variables != nil {
		if err := c.h.st.SetVariables(store.VarOwnerEnvironment, e.UID, *in.Variables); err != nil {
			_ = c.h.st.DeleteEnvironment(e.UID) // keep create all-or-nothing
			collErr(w, err)
			return
		}
	}
	v, err := c.envView(*e, false)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (c *collectionsAPI) getEnv(w http.ResponseWriter, r *http.Request) {
	e, err := c.h.st.GetEnvironment(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	v, err := c.envView(*e, revealSecrets(r))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (c *collectionsAPI) updateEnv(w http.ResponseWriter, r *http.Request) {
	var in envInput
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	e, err := c.h.st.UpdateEnvironment(store.Environment{UID: r.PathValue("uid"), Name: in.Name,
		BoundIdentity: in.BoundIdentity, BaseTargetPin: in.BaseTargetPin, Rev: in.Rev})
	if err != nil {
		collErr(w, err)
		return
	}
	if in.Variables != nil {
		if len(*in.Variables) > maxVariablesBatch {
			httpErr(w, http.StatusBadRequest, "too many variables")
			return
		}
		if err := c.h.st.SetVariables(store.VarOwnerEnvironment, e.UID, *in.Variables); err != nil {
			collErr(w, err)
			return
		}
	}
	v, err := c.envView(*e, false)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (c *collectionsAPI) deleteEnv(w http.ResponseWriter, r *http.Request) {
	if err := c.h.st.DeleteEnvironment(r.PathValue("uid")); err != nil {
		collErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- variables ---------------------------------------------------------------

// ownerExists checks that a variable owner is a real object, so the variable
// tables cannot collect rows for arbitrary ids.
func (c *collectionsAPI) ownerExists(kind, uid string) error {
	var err error
	switch kind {
	case store.VarOwnerEnvironment:
		_, err = c.h.st.GetEnvironment(uid)
	case store.VarOwnerCollection:
		_, err = c.h.st.GetCollection(uid)
	case store.VarOwnerFolder, store.VarOwnerRequest:
		var it *store.Item
		if it, err = c.h.st.GetItem(uid); err == nil {
			if (kind == store.VarOwnerFolder) != (it.Kind == "folder") {
				err = store.ErrCollInvalid
			}
		}
	case store.VarOwnerGlobal:
		// Globals have no owning row; the uid is a free label.
		if strings.TrimSpace(uid) == "" || len(uid) > 128 {
			err = store.ErrCollInvalid
		}
	default:
		err = store.ErrCollInvalid
	}
	return err
}

func (c *collectionsAPI) getVars(w http.ResponseWriter, r *http.Request) {
	kind, uid := r.PathValue("kind"), r.PathValue("uid")
	if err := c.ownerExists(kind, uid); err != nil {
		collErr(w, err)
		return
	}
	vs, err := c.viewVars(kind, uid, revealSecrets(r))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": vs})
}

func (c *collectionsAPI) putVars(w http.ResponseWriter, r *http.Request) {
	kind, uid := r.PathValue("kind"), r.PathValue("uid")
	var in struct {
		Variables []store.Variable `json:"variables"`
	}
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if len(in.Variables) > maxVariablesBatch {
		httpErr(w, http.StatusBadRequest, "too many variables")
		return
	}
	if err := c.ownerExists(kind, uid); err != nil {
		collErr(w, err)
		return
	}
	for i := range in.Variables {
		in.Variables[i].OwnerKind, in.Variables[i].OwnerUID = kind, uid
	}
	c.mu.Lock()
	err := c.h.st.SetVariables(kind, uid, in.Variables)
	c.mu.Unlock()
	if err != nil {
		collErr(w, err)
		return
	}
	vs, err := c.viewVars(kind, uid, false)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": vs})
}

// setCurrent stores a local current value, declaring the variable first when
// it is new. The response never echoes a secret's value.
func (c *collectionsAPI) setCurrentValue(kind, uid, key, value, by string, secret bool) (secretOut bool, err error) {
	if err = c.ownerExists(kind, uid); err != nil {
		return false, err
	}
	if strings.TrimSpace(key) == "" || len(key) > 256 {
		return false, store.ErrCollInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	decl, err := c.h.st.ListVariables(kind, uid)
	if err != nil {
		return false, err
	}
	found := false
	for _, d := range decl {
		if d.Key == key {
			found, secretOut = true, d.Type == store.VarTypeSecret
		}
	}
	if !found {
		if len(decl) >= maxVariablesBatch {
			return false, store.ErrCollInvalid
		}
		typ := store.VarTypeDefault
		if secret || store.IsSecretName(key) {
			typ, secretOut = store.VarTypeSecret, true
		}
		decl = append(decl, store.Variable{OwnerKind: kind, OwnerUID: uid, Key: key, Type: typ, Enabled: true})
		if err = c.h.st.SetVariables(kind, uid, decl); err != nil {
			return false, err
		}
	}
	if secretOut {
		c.reg.Add(value)
	}
	return secretOut, c.h.st.SetCurrentValue(kind, uid, key, value, by)
}

func (c *collectionsAPI) putCurrent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Secret bool   `json:"secret"`
	}
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	by := "user"
	if isAISource(r) {
		by = "ai"
	}
	secret, err := c.setCurrentValue(r.PathValue("kind"), r.PathValue("uid"), in.Key, in.Value, by, in.Secret)
	if err != nil {
		collErr(w, err)
		return
	}
	out := map[string]any{"ok": true, "key": in.Key, "secret": secret}
	if !secret {
		out["value"] = in.Value
	}
	writeJSON(w, http.StatusOK, out)
}

func (c *collectionsAPI) resetCurrent(w http.ResponseWriter, r *http.Request) {
	kind, uid := r.PathValue("kind"), r.PathValue("uid")
	if err := c.ownerExists(kind, uid); err != nil {
		collErr(w, err)
		return
	}
	if err := c.h.st.ResetCurrentValues(kind, uid); err != nil {
		httpInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolvePreview resolves one template against the real layers. Secret values
// are masked in the answer.
func (c *collectionsAPI) resolvePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template      string `json:"template"`
		CollectionUID string `json:"collectionUid"`
		ItemUID       string `json:"itemUid"`
		EnvUID        string `json:"envUid"`
	}
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	var chain collexec.Chain
	switch {
	case in.ItemUID != "":
		ch, err := collexec.LoadChain(c.h.st, in.ItemUID)
		if err != nil {
			httpErr(w, http.StatusNotFound, "item not found")
			return
		}
		chain = ch
	case in.CollectionUID != "":
		co, err := c.h.st.GetCollection(in.CollectionUID)
		if err != nil {
			collErr(w, err)
			return
		}
		chain = collexec.Chain{Collection: *co}
	default:
		httpErr(w, http.StatusBadRequest, "collectionUid or itemUid required")
		return
	}
	layers, _, err := c.backend().Layers(chain, in.EnvUID)
	if err != nil {
		collErr(w, err)
		return
	}
	stack := varstore.NewStack(layers...)
	stack.RegisterSecrets(c.reg)
	res := varstore.New(varstore.Options{Policy: varstore.PolicyLiteral, Registry: c.reg}).Resolve(in.Template, stack)
	out := map[string]any{"value": c.reg.Mask(res.Value), "uses": res.Uses, "unresolved": res.Unresolved}
	if res.Err != nil {
		out["error"] = res.Err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
