// Package oob is an out-of-band interaction catcher for blind-vulnerability
// testing (blind SSRF / XXE / SQLi / RCE). It mints unique tokens, records any
// inbound HTTP request whose path carries one (/oob/<token>/…), and lets the UI
// poll the captured interactions.
//
// Reachability is the operator's responsibility: the catcher only sees callbacks
// that actually reach the bound interface. For local testing the control origin
// works; for a real target, bind externally (or tunnel) and set the base URL so
// payloads point at a host the target can resolve and reach. (This is the same
// constraint any collaborator has — we just don't ship a public server.)
package oob

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// oobFallbackSeq backs Token's fallback when crypto/rand is unavailable.
var oobFallbackSeq atomic.Uint64

const maxInteractions = 500

// Interaction is one recorded inbound hit.
type Interaction struct {
	ID         int64  `json:"id"`
	Token      string `json:"token"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	RemoteAddr string `json:"remoteAddr"`
	Host       string `json:"host"`
	UserAgent  string `json:"userAgent"`
	Query      string `json:"query"`
	BodyPrev   string `json:"bodyPreview"`
	TS         int64  `json:"ts"` // unix millis
}

// Catcher records OOB interactions in memory.
type Catcher struct {
	mu     sync.Mutex
	items  []Interaction
	seq    int64
	notify func()
}

// New returns an empty Catcher.
func New() *Catcher { return &Catcher{} }

// SetNotifier registers a callback fired when a new interaction is recorded.
func (c *Catcher) SetNotifier(fn func()) {
	c.mu.Lock()
	c.notify = fn
	c.mu.Unlock()
}

// Token mints a fresh random token to embed in an OOB payload URL.
func (c *Catcher) Token() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand is essentially never unavailable post-boot; if it ever is,
		// use a monotonic counter — unique and not wall-clock-guessable/collidable.
		binary.BigEndian.PutUint64(b, oobFallbackSeq.Add(1))
	}
	return hex.EncodeToString(b)
}

// TokenFromPath extracts the token from a /oob/<token>[/...] request path, or "".
func TokenFromPath(p string) string {
	p = strings.TrimPrefix(p, "/oob/")
	if p == "" || strings.HasPrefix(p, "/oob") {
		return ""
	}
	if i := strings.IndexAny(p, "/?"); i >= 0 {
		p = p[:i]
	}
	return p
}

// Record stores an interaction for the request (token taken from its path).
// bodyPreview should be a short, already-truncated snippet (may be empty).
func (c *Catcher) Record(r *http.Request, bodyPreview string) {
	tok := TokenFromPath(r.URL.Path)
	if tok == "" {
		return
	}
	it := Interaction{
		Token:      tok,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Host:       r.Host,
		UserAgent:  r.UserAgent(),
		Query:      r.URL.RawQuery,
		BodyPrev:   bodyPreview,
		TS:         time.Now().UnixMilli(),
	}
	c.mu.Lock()
	c.seq++
	it.ID = c.seq
	c.items = append(c.items, it)
	if len(c.items) > maxInteractions {
		c.items = c.items[len(c.items)-maxInteractions:]
	}
	fn := c.notify
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// List returns the recorded interactions, newest first.
func (c *Catcher) List() []Interaction {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Interaction, len(c.items))
	for i, it := range c.items {
		out[len(c.items)-1-i] = it
	}
	return out
}

// Count returns how many interactions are stored.
func (c *Catcher) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Clear drops all recorded interactions.
func (c *Catcher) Clear() {
	c.mu.Lock()
	c.items = nil
	c.mu.Unlock()
}
