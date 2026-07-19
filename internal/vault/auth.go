package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TokenScope is "full" (read+write) or "read".
type TokenScope string

const (
	ScopeFull TokenScope = "full"
	ScopeRead TokenScope = "read"
)

type tokenRecord struct {
	ID     string     `json:"id"`
	Prefix string     `json:"prefix"`
	Hash   string     `json:"hash"`
	Scope  TokenScope `json:"scope"`
	Label  string     `json:"label,omitempty"`
}

// Auth manages vault bearer tokens (hashed at rest).
type Auth struct {
	mu   sync.Mutex
	path string
	toks []tokenRecord
}

func OpenAuth(dir string) (*Auth, error) {
	a := &Auth{path: filepath.Join(dir, "tokens.json")}
	if b, err := os.ReadFile(a.path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &a.toks)
	}
	return a, nil
}

func (a *Auth) saveLocked() error {
	b, err := json.MarshalIndent(a.toks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.path, b, 0o600)
}

// EnsureBootstrap creates a full-scope token if none exist. Returns the raw
// token (only shown once) and whether it was newly created.
func (a *Auth) EnsureBootstrap() (raw string, created bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.toks) > 0 {
		return "", false, nil
	}
	return a.createLocked(ScopeFull, "bootstrap")
}

// Create mints a new token.
func (a *Auth) Create(scope TokenScope, label string) (raw string, rec tokenRecord, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if scope != ScopeFull && scope != ScopeRead {
		scope = ScopeFull
	}
	raw, _, err = a.createLocked(scope, label)
	if err != nil {
		return "", tokenRecord{}, err
	}
	return raw, a.toks[len(a.toks)-1], nil
}

func (a *Auth) createLocked(scope TokenScope, label string) (string, bool, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, err
	}
	raw := "iv_" + hex.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(raw))
	id := hex.EncodeToString(sum[:8])
	rec := tokenRecord{
		ID: id, Prefix: raw[:10], Hash: hex.EncodeToString(sum[:]),
		Scope: scope, Label: label,
	}
	a.toks = append(a.toks, rec)
	if err := a.saveLocked(); err != nil {
		a.toks = a.toks[:len(a.toks)-1]
		return "", false, err
	}
	// Also write plaintext bootstrap file for operators (0600).
	if label == "bootstrap" {
		_ = os.WriteFile(filepath.Join(filepath.Dir(a.path), "vault.token"), []byte(raw+"\n"), 0o600)
	}
	return raw, true, nil
}

// Check validates Authorization bearer and returns scope.
func (a *Auth) Check(authHeader string) (TokenScope, error) {
	const pfx = "Bearer "
	if !strings.HasPrefix(authHeader, pfx) {
		return "", fmt.Errorf("missing bearer token")
	}
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, pfx))
	if raw == "" {
		return "", fmt.Errorf("missing bearer token")
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.toks {
		if t.Hash == hash {
			return t.Scope, nil
		}
	}
	return "", fmt.Errorf("invalid token")
}

// HasTokens reports whether any token is configured.
func (a *Auth) HasTokens() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.toks) > 0
}
