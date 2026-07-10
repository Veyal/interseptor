package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// API-key scopes. A read key may only read (GET/SSE); a full key may also mutate
// and drive traffic. Unknown/empty normalizes to full for back-compat.
const (
	ScopeFull = "full"
	ScopeRead = "read"
)

// NormalizeScope maps any input to a known scope (default full).
func NormalizeScope(s string) string {
	if s == ScopeRead {
		return ScopeRead
	}
	return ScopeFull
}

// APIKey is metadata for an issued control-API key. The secret token itself is
// never stored — only its SHA-256 hash and a short identifying prefix.
type APIKey struct {
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Prefix  string `json:"prefix"`
	Created int64  `json:"created"`           // unix millis
	Scope   string `json:"scope"`             // "full" | "read"
	Expires int64  `json:"expires,omitempty"` // unix millis; 0 = never
}

// CreateAPIKey mints a new key, returning the full token (shown to the user
// once) and its stored metadata. The token is "ick_" + 48 hex chars. scope is
// normalized to full/read; expires is a unix-millis expiry (0 = never).
func (s *Store) CreateAPIKey(label, scope string, expires int64) (token string, key APIKey, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", APIKey{}, err
	}
	token = "ick_" + hex.EncodeToString(buf)
	prefix := token[:12]
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UnixMilli()
	scope = NormalizeScope(scope)
	if expires < 0 {
		expires = 0
	}

	res, err := s.keysDB().Exec(
		`INSERT INTO api_keys (label, prefix, hash, created, scope, expires) VALUES (?,?,?,?,?,?)`,
		label, prefix, hash, now, scope, expires)
	if err != nil {
		return "", APIKey{}, err
	}
	id, _ := res.LastInsertId()
	return token, APIKey{ID: id, Label: label, Prefix: prefix, Created: now, Scope: scope, Expires: expires}, nil
}

// ListAPIKeys returns all key metadata (never the token or hash), newest first.
func (s *Store) ListAPIKeys() ([]APIKey, error) {
	rows, err := s.keysDB().Query(`SELECT id, label, prefix, created, scope, expires FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Label, &k.Prefix, &k.Created, &k.Scope, &k.Expires); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey revokes a key by id.
func (s *Store) DeleteAPIKey(id int64) error {
	_, err := s.keysDB().Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// HasAPIKeys reports whether any control-API key exists. Auth is opt-in: while
// this is false the MCP endpoint stays open (loopback trust); once the operator
// creates a key, a valid bearer token is required.
func (s *Store) HasAPIKeys() (bool, error) {
	var n int
	if err := s.keysDB().QueryRow(`SELECT COUNT(1) FROM api_keys`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// VerifyAPIKey reports whether token matches a stored, unexpired key. Retained
// for the MCP opt-in gate and existing callers; new code that needs the scope
// should call VerifyAPIKeyScope.
func (s *Store) VerifyAPIKey(token string) (bool, error) {
	ok, _, err := s.VerifyAPIKeyScope(token)
	return ok, err
}

// VerifyAPIKeyScope reports whether token matches a stored key, and if so its
// scope. An expired key (expires != 0 && expires <= now) verifies as false.
func (s *Store) VerifyAPIKeyScope(token string) (ok bool, scope string, err error) {
	if token == "" {
		return false, "", nil
	}
	sum := sha256.Sum256([]byte(token))
	var expires int64
	err = s.keysDB().QueryRow(
		`SELECT scope, expires FROM api_keys WHERE hash = ?`,
		hex.EncodeToString(sum[:]),
	).Scan(&scope, &expires)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return false, "", nil
		}
		return false, "", err
	}
	if expires != 0 && expires <= time.Now().UnixMilli() {
		return false, "", nil
	}
	return true, NormalizeScope(scope), nil
}
