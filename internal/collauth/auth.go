package collauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/redact"
)

// Request is the mutable wire request an authenticator signs. Headers are
// canonical http.Header; Body is the final body (used by digest auth-int,
// SigV4 and JWT body claims only through hashing).
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Config is one auth configuration with already-resolved field values.
type Config struct {
	Type   string
	Fields map[string]string
}

// fieldAliases maps this package's field names to the snake_case spellings the
// Postman and OpenAPI importers write, so imported auth works as written.
var fieldAliases = map[string]string{"grantType": "grant_type", "clientAuth": "client_authentication"}

func (c Config) f(name string) string {
	if v := strings.TrimSpace(c.Fields[name]); v != "" {
		return v
	}
	if alias, ok := fieldAliases[name]; ok {
		return strings.TrimSpace(c.Fields[alias])
	}
	return ""
}

// tokenInQuery reports whether the token goes into the query string
// ("query", or Postman's "queryParams").
func (c Config) tokenInQuery() bool {
	switch strings.ToLower(c.f("addTokenTo")) {
	case "query", "queryparams", "url":
		return true
	}
	return false
}

// Result describes what Apply did. It never contains secret values.
type Result struct {
	Applied   string   // e.g. "auth:oauth2"
	Refreshed bool     // a token was fetched or refreshed
	Warnings  []string // non-fatal notes
}

// Doer performs an HTTP call (token endpoints, digest probes). The caller
// passes a sender-backed implementation so scope guards apply.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Applier is the interface collexec depends on. key identifies the token
// cache slot (for example a collection or item id).
type Applier interface {
	Apply(ctx context.Context, key string, cfg Config, req *Request) (Result, error)
}

// ErrUnsupported reports an auth type this package does not implement.
var ErrUnsupported = errors.New("collauth: unsupported auth type")

// Manager implements Applier.
type Manager struct {
	doer    Doer
	store   TokenStore
	secrets *redact.Registry
	now     func() time.Time
	pending *pendingAuth
}

// Options configures a Manager. Zero values pick safe defaults.
type Options struct {
	Doer    Doer
	Store   TokenStore
	Secrets *redact.Registry
	Now     func() time.Time
}

// New builds a Manager.
func New(o Options) *Manager {
	m := &Manager{doer: o.Doer, store: o.Store, secrets: o.Secrets, now: o.Now}
	if m.doer == nil {
		m.doer = &http.Client{Timeout: 30 * time.Second}
	}
	if m.store == nil {
		m.store = NewMemoryStore()
	}
	if m.secrets == nil {
		m.secrets = redact.NewRegistry()
	}
	if m.now == nil {
		m.now = time.Now
	}
	m.pending = newPending(m.now)
	return m
}

// Secrets returns the registry holding every secret this manager has seen.
func (m *Manager) Secrets() *redact.Registry { return m.secrets }

// Scrub masks every known secret in s.
func (m *Manager) Scrub(s string) string { return m.secrets.Mask(s) }

func (m *Manager) track(vals ...string) {
	for _, v := range vals {
		if v != "" {
			m.secrets.Add(v)
		}
	}
}

// Apply signs or decorates req according to cfg. An explicit Authorization
// header already on the request is never overwritten (Postman parity).
func (m *Manager) Apply(ctx context.Context, key string, cfg Config, req *Request) (Result, error) {
	if req.Header == nil {
		req.Header = http.Header{}
	}
	typ := strings.ToLower(cfg.Type)
	switch typ {
	case "", "none", "noauth", "inherit":
		return Result{}, nil
	case "basic":
		return m.applyBasic(cfg, req)
	case "bearer":
		return m.applyBearer(cfg, req)
	case "apikey":
		return m.applyAPIKey(cfg, req)
	case "jwt":
		return m.applyJWT(cfg, req)
	case "digest":
		return m.applyDigest(ctx, cfg, req)
	case "awsv4", "sigv4", "aws":
		return m.applySigV4(cfg, req)
	case "oauth2":
		return m.applyOAuth2(ctx, key, cfg, req)
	}
	return Result{}, fmt.Errorf("%w: %q", ErrUnsupported, cfg.Type)
}

func (m *Manager) fail(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(m.Scrub(err.Error()))
}
