package collrun

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/collauth"
	"github.com/Veyal/interseptor/internal/store"
)

// tokenStore persists OAuth2 tokens in ix_tokens so a restart or a CLI run can
// reuse them. Rows hold raw tokens: they are local-only and removed by the
// shared snapshot scrub (store.ScrubSnapshotFile), exactly like cookies and
// current values. A cache key is "collectionUid|..."; the first segment picks
// the collection row.
type tokenStore struct {
	st *store.Store
	mu sync.Mutex
}

var _ collauth.TokenStore = (*tokenStore)(nil)

type tokenRecord struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh,omitempty"`
	Type    string `json:"type,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Expiry  int64  `json:"expiry,omitempty"` // unix ms; 0 = unknown
}

func collOf(key string) string {
	if i := strings.IndexByte(key, '|'); i > 0 {
		return key[:i]
	}
	return ""
}

func (s *tokenStore) Get(key string) (collauth.Token, bool) {
	coll := collOf(key)
	if coll == "" {
		return collauth.Token{}, false
	}
	rows, err := s.st.ListTokens(coll)
	if err != nil {
		return collauth.Token{}, false
	}
	for _, r := range rows {
		if r.Name != key {
			continue
		}
		var rec tokenRecord
		if json.Unmarshal([]byte(r.TokenJSON), &rec) != nil || rec.Access == "" {
			return collauth.Token{}, false
		}
		tok := collauth.Token{Access: rec.Access, Refresh: rec.Refresh, Type: rec.Type, Scope: rec.Scope}
		if rec.Expiry > 0 {
			tok.Expiry = time.UnixMilli(rec.Expiry)
		}
		return tok, true
	}
	return collauth.Token{}, false
}

func (s *tokenStore) Put(key string, t collauth.Token) {
	coll := collOf(key)
	if coll == "" {
		return
	}
	rec := tokenRecord{Access: t.Access, Refresh: t.Refresh, Type: t.Type, Scope: t.Scope}
	if !t.Expiry.IsZero() {
		rec.Expiry = t.Expiry.UnixMilli()
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := store.CollToken{CollectionUID: coll, Name: key, TokenJSON: string(raw), Expires: rec.Expiry}
	if rows, err := s.st.ListTokens(coll); err == nil {
		for _, r := range rows {
			if r.Name == key {
				row.UID = r.UID
			}
		}
	}
	_, _ = s.st.PutToken(row)
}

func (s *tokenStore) Delete(key string) {
	if coll := collOf(key); coll != "" {
		_ = s.st.DeleteToken(coll, key)
	}
}
