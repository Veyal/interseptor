package collauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	refreshSkew   = 30 * time.Second
	pendingTTL    = 10 * time.Minute
	maxTokenBody  = 1 << 20
	maxPendingLen = 64
)

func (m *Manager) applyOAuth2(ctx context.Context, key string, cfg Config, req *Request) (Result, error) {
	m.track(cfg.Fields["clientSecret"], cfg.Fields["password"], cfg.Fields["refreshToken"], cfg.Fields["accessToken"])
	res := Result{Applied: "auth:oauth2"}
	tok, ok := m.store.Get(key)
	if !ok && cfg.f("accessToken") != "" {
		tok, ok = Token{Access: cfg.f("accessToken"), Type: cfg.f("tokenType"), Refresh: cfg.f("refreshToken")}, true
	}
	grant := strings.ToLower(cfg.f("grantType"))
	if grant == "" {
		grant = "client_credentials"
	}
	if ok && tok.Access != "" && !tok.Expired(m.now(), refreshSkew) {
		return m.attachToken(cfg, req, tok, res)
	}
	// Expired or missing: refresh, else re-run non-interactive grants.
	var err error
	switch {
	case ok && tok.Refresh != "" && cfg.f("accessTokenUrl") != "":
		tok, err = m.tokenRequest(ctx, cfg, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.Refresh}}, tok.Refresh)
	case grant == "client_credentials" || grant == "password":
		tok, err = m.fetchGrant(ctx, cfg, grant)
	case ok && tok.Access != "":
		res.Warnings = append(res.Warnings, "oauth2 token is expired and cannot be refreshed; sending it anyway")
		return m.attachToken(cfg, req, tok, res)
	default:
		return Result{}, errors.New("oauth2: no token; authorize first (authorization_code needs interactive consent)")
	}
	if err != nil {
		return Result{}, m.fail(err)
	}
	m.store.Put(key, tok)
	res.Refreshed = true
	return m.attachToken(cfg, req, tok, res)
}

func (m *Manager) attachToken(cfg Config, req *Request, t Token, res Result) (Result, error) {
	m.track(t.Access, t.Refresh)
	if cfg.tokenInQuery() {
		k := cfg.f("queryParamKey")
		if k == "" {
			k = "access_token"
		}
		return res, addQuery(req, k, t.Access)
	}
	if hasAuthz(req) {
		return Result{}, nil
	}
	prefix := cfg.f("headerPrefix")
	if prefix == "" {
		prefix = t.Type
	}
	if prefix == "" || strings.EqualFold(prefix, "bearer") {
		prefix = "Bearer"
	}
	req.Header.Set("Authorization", prefix+" "+t.Access)
	return res, nil
}

func (m *Manager) fetchGrant(ctx context.Context, cfg Config, grant string) (Token, error) {
	if cfg.f("accessTokenUrl") == "" {
		return Token{}, errors.New("oauth2: accessTokenUrl is required")
	}
	v := url.Values{"grant_type": {grant}}
	if grant == "password" {
		v.Set("username", cfg.Fields["username"])
		v.Set("password", cfg.Fields["password"])
	}
	return m.tokenRequest(ctx, cfg, v, "")
}

// tokenRequest posts to the token endpoint. prevRefresh is kept when the
// server omits a new refresh token (RFC 6749 section 6).
func (m *Manager) tokenRequest(ctx context.Context, cfg Config, v url.Values, prevRefresh string) (Token, error) {
	if s := cfg.f("scope"); s != "" {
		v.Set("scope", s)
	}
	if a := cfg.f("audience"); a != "" {
		v.Set("audience", a)
	}
	id, secret := cfg.f("clientId"), cfg.Fields["clientSecret"]
	basic := !strings.EqualFold(cfg.f("clientAuth"), "body")
	if !basic {
		v.Set("client_id", id)
		if secret != "" {
			v.Set("client_secret", secret)
		}
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.f("accessTokenUrl"), strings.NewReader(v.Encode()))
	if err != nil {
		return Token{}, errors.New("oauth2: invalid accessTokenUrl")
	}
	hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hr.Header.Set("Accept", "application/json")
	if basic && id != "" {
		hr.SetBasicAuth(url.QueryEscape(id), url.QueryEscape(secret))
	}
	resp, err := m.doer.Do(hr)
	if err != nil {
		return Token{}, fmt.Errorf("oauth2 token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if resp.StatusCode/100 != 2 {
		return Token{}, fmt.Errorf("oauth2 token endpoint returned %d: %s", resp.StatusCode, snippet(body))
	}
	var tr struct {
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		TokenType    string      `json:"token_type"`
		Scope        string      `json:"scope"`
		ExpiresIn    json.Number `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return Token{}, errors.New("oauth2: token response has no access_token")
	}
	t := Token{Access: tr.AccessToken, Refresh: tr.RefreshToken, Type: tr.TokenType, Scope: tr.Scope}
	if t.Refresh == "" {
		t.Refresh = prevRefresh
	}
	if n, err := tr.ExpiresIn.Float64(); err == nil && n > 0 {
		t.Expiry = m.now().Add(time.Duration(n * float64(time.Second)))
	}
	m.track(t.Access, t.Refresh)
	return t, nil
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ---- authorization code + PKCE ----

type pendingEntry struct {
	key, verifier, redirect string
	cfg                     Config
	created                 time.Time
}

type pendingAuth struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]pendingEntry
}

func newPending(now func() time.Time) *pendingAuth {
	return &pendingAuth{now: now, m: map[string]pendingEntry{}}
}

func (p *pendingAuth) add(state string, e pendingEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.m {
		if p.now().Sub(v.created) > pendingTTL {
			delete(p.m, k)
		}
	}
	if len(p.m) >= maxPendingLen {
		for k := range p.m {
			delete(p.m, k)
			break
		}
	}
	p.m[state] = e
}

// take returns and removes the entry (one-shot state).
func (p *pendingAuth) take(state string) (pendingEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[state]
	delete(p.m, state)
	if !ok || p.now().Sub(e.created) > pendingTTL {
		return pendingEntry{}, false
	}
	return e, true
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCEChallenge returns the S256 code challenge for verifier.
func PKCEChallenge(verifier string) string {
	s := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

// BeginAuthCode starts an authorization-code flow (PKCE S256 unless
// cfg pkce=false). redirectURI must be the control-port callback URL served by
// CallbackHandler. It returns the URL to open and the one-shot state.
func (m *Manager) BeginAuthCode(key string, cfg Config, redirectURI string) (authURL, state string, err error) {
	base := cfg.f("authUrl")
	if base == "" || cfg.f("accessTokenUrl") == "" || redirectURI == "" {
		return "", "", errors.New("oauth2: authUrl, accessTokenUrl and redirect URI are required")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "", errors.New("oauth2: invalid authUrl")
	}
	m.track(cfg.Fields["clientSecret"])
	state = randToken(24)
	verifier := ""
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.f("clientId"))
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if s := cfg.f("scope"); s != "" {
		q.Set("scope", s)
	}
	if cfg.f("pkce") != "false" {
		verifier = randToken(48)
		q.Set("code_challenge", PKCEChallenge(verifier))
		q.Set("code_challenge_method", "S256")
	}
	u.RawQuery = q.Encode()
	m.pending.add(state, pendingEntry{key: key, verifier: verifier, redirect: redirectURI, cfg: cfg, created: m.now()})
	return u.String(), state, nil
}

// CompleteAuthCode exchanges the code for tokens and stores them.
func (m *Manager) CompleteAuthCode(ctx context.Context, state, code string) error {
	e, ok := m.pending.take(state)
	if !ok {
		return errors.New("oauth2: unknown or expired state")
	}
	if code == "" {
		return errors.New("oauth2: empty authorization code")
	}
	v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {e.redirect}}
	if e.verifier != "" {
		v.Set("code_verifier", e.verifier)
	}
	tok, err := m.tokenRequest(ctx, e.cfg, v, "")
	if err != nil {
		return m.fail(err)
	}
	m.store.Put(e.key, tok)
	return nil
}

// CallbackHandler serves the redirect target. Mount it on the control port
// (for example at /api/collections/oauth/callback). It shows no token data.
func (m *Manager) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			m.pending.take(q.Get("state"))
			http.Error(w, "Authorization failed: "+m.Scrub(snippet([]byte(e))), http.StatusBadRequest)
			return
		}
		if err := m.CompleteAuthCode(r.Context(), q.Get("state"), q.Get("code")); err != nil {
			http.Error(w, "Authorization failed.", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "Authorized. You can close this tab.")
	})
}
