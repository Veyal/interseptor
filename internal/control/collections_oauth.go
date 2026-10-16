package control

import (
	"net/http"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
)

// oauthCallbackPath is where the identity provider redirects the browser. It
// lives on the control port next to the rest of the API, so it is behind the
// same host and session guards as every other route.
const oauthCallbackPath = "/api/collections/oauth/callback"

const (
	maxPendingOAuth = 64
	oauthStateTTL   = 10 * time.Minute
)

// oauthPending remembers, per authorization-code state, which collection it was
// started for, so the token exchange at callback time runs under that
// collection's scope policy (the exchange happens outside any request Step).
type oauthPending struct {
	mu sync.Mutex
	m  map[string]oauthEntry
}

type oauthEntry struct {
	in      collexec.StepInput
	created time.Time
}

func (p *oauthPending) put(state string, in collexec.StepInput) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]oauthEntry{}
	}
	for k, v := range p.m {
		if time.Since(v.created) > oauthStateTTL {
			delete(p.m, k)
		}
	}
	for len(p.m) >= maxPendingOAuth {
		for k := range p.m {
			delete(p.m, k)
			break
		}
	}
	p.m[state] = oauthEntry{in: in, created: time.Now()}
}

// peek returns the entry without consuming it: the collauth manager owns the
// one-shot state and consumes it when it completes the exchange.
func (p *oauthPending) peek(state string) (collexec.StepInput, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[state]
	if !ok || time.Since(e.created) > oauthStateTTL {
		delete(p.m, state)
		return collexec.StepInput{}, false
	}
	return e.in, true
}

func (p *oauthPending) drop(state string) {
	p.mu.Lock()
	delete(p.m, state)
	p.mu.Unlock()
}

type oauthBeginRequest struct {
	ItemUID string            `json:"itemUid"`
	EnvUID  string            `json:"envUid"`
	Local   map[string]string `json:"local"`
}

// oauthBegin starts an authorization-code (+PKCE) flow for the request item's
// effective OAuth2 auth and returns the URL the person must open. Granting a
// token is an interactive act: UI session only.
func (c *collectionsAPI) oauthBegin(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	var in oauthBeginRequest
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if in.ItemUID == "" {
		httpErr(w, http.StatusBadRequest, "itemUid required")
		return
	}
	chain, err := collexec.LoadChain(c.h.st, in.ItemUID)
	if err != nil {
		collErr(w, err)
		return
	}
	be := c.backend()
	layers, local, err := be.Layers(chain, in.EnvUID)
	if err != nil {
		collErr(w, err)
		return
	}
	for k, v := range in.Local {
		if local == nil {
			local = map[string]string{}
		}
		local[k] = v
	}
	step := collexec.StepInput{Chain: chain, Layers: layers, Local: local, Source: collexec.SourceUI,
		ScopePolicy: store.ScopePolicyBlock, EnvUID: in.EnvUID}
	cfg, key, err := collexec.ResolveAuth(step, c.reg)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if cfg.Type != "oauth2" {
		httpErr(w, http.StatusBadRequest, "the request's effective auth is not OAuth 2.0")
		return
	}
	redirect := "http://" + r.Host + oauthCallbackPath
	authURL, state, err := be.Auth().BeginAuthCode(key, cfg, redirect)
	if err != nil {
		httpErr(w, http.StatusBadRequest, be.Scrub(err.Error()))
		return
	}
	// The exchange runs under the collection's policy (block unless a person
	// chose otherwise) with the interactive source.
	step.ScopePolicy = ""
	c.oauth.put(state, step)
	writeJSON(w, http.StatusOK, map[string]any{"authUrl": authURL, "state": state, "redirectUri": redirect})
}

// oauthCallback receives the redirect from the identity provider and completes
// the exchange. It never shows token data.
func (c *collectionsAPI) oauthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	in, ok := c.oauth.peek(state)
	if !ok {
		http.Error(w, "Authorization failed.", http.StatusBadRequest)
		return
	}
	defer c.oauth.drop(state)
	ctx := c.backend().AuthContext(r.Context(), in)
	c.backend().Auth().CallbackHandler().ServeHTTP(w, r.WithContext(ctx))
}
