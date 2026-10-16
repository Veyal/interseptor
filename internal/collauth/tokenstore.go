package collauth

import (
	"encoding/json"
	"sync"
	"time"
)

// Token is an OAuth2 token set. Its JSON and string forms never contain the
// secret values; use Access/Refresh fields directly in memory only.
type Token struct {
	Access  string
	Refresh string
	Type    string
	Scope   string
	Expiry  time.Time // zero means no known expiry
}

// MarshalJSON redacts secrets so a Token can never leak through logs,
// exports, archives or MCP output.
func (t Token) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string    `json:"type,omitempty"`
		Scope      string    `json:"scope,omitempty"`
		Expiry     time.Time `json:"expiry,omitzero"`
		HasAccess  bool      `json:"hasAccess"`
		HasRefresh bool      `json:"hasRefresh"`
	}{t.Type, t.Scope, t.Expiry, t.Access != "", t.Refresh != ""})
}

// String is redacted.
func (t Token) String() string { return "Token{redacted}" }

// Expired reports whether the token expires within skew of now.
func (t Token) Expired(now time.Time, skew time.Duration) bool {
	return !t.Expiry.IsZero() && !now.Add(skew).Before(t.Expiry)
}

// TokenStore persists tokens by key. Implementations must be safe for
// concurrent use. Persistent implementations must encrypt or keep the data
// local-only; the in-memory store is the default.
type TokenStore interface {
	Get(key string) (Token, bool)
	Put(key string, t Token)
	Delete(key string)
}

// MemoryStore is a mutex-guarded in-memory TokenStore.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]Token
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]Token{}} }

func (s *MemoryStore) Get(key string) (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[key]
	return t, ok
}

func (s *MemoryStore) Put(key string, t Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = t
}

func (s *MemoryStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}
