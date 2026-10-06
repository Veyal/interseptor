package store

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// IPAllowEntry is one machine-global client IP/CIDR allowed without an API key.
type IPAllowEntry struct {
	ID      int64  `json:"id"`
	CIDR    string `json:"cidr"`
	Label   string `json:"label,omitempty"`
	Created int64  `json:"created"`
}

// NormalizeAllowCIDR validates and canonicalizes a single IP or CIDR string.
// Bare addresses become /32 (IPv4, including IPv4-mapped IPv6) or /128.
func NormalizeAllowCIDR(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("cidr required")
	}
	if strings.Contains(raw, "/") {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return "", fmt.Errorf("invalid CIDR: %w", err)
		}
		return n.String(), nil
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return "", fmt.Errorf("invalid IP address")
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String() + "/32", nil
	}
	return ip.String() + "/128", nil
}

// AddIPAllowlist inserts a CIDR/IP. Duplicate cidr returns an error.
func (s *Store) AddIPAllowlist(cidr, label string) (IPAllowEntry, error) {
	norm, err := NormalizeAllowCIDR(cidr)
	if err != nil {
		return IPAllowEntry{}, err
	}
	now := time.Now().UnixMilli()
	res, err := s.keysDB().Exec(
		`INSERT INTO ip_allowlist (cidr, label, created) VALUES (?,?,?)`,
		norm, strings.TrimSpace(label), now)
	if err != nil {
		return IPAllowEntry{}, err
	}
	s.allow.invalidate()
	id, _ := res.LastInsertId()
	return IPAllowEntry{ID: id, CIDR: norm, Label: strings.TrimSpace(label), Created: now}, nil
}

// ListIPAllowlist returns all allowlist entries, newest first.
func (s *Store) ListIPAllowlist() ([]IPAllowEntry, error) {
	rows, err := s.keysDB().Query(`SELECT id, cidr, label, created FROM ip_allowlist ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPAllowEntry
	for rows.Next() {
		var e IPAllowEntry
		var label *string
		if err := rows.Scan(&e.ID, &e.CIDR, &label, &e.Created); err != nil {
			return nil, err
		}
		if label != nil {
			e.Label = *label
		}
		out = append(out, e)
	}
	if out == nil {
		out = []IPAllowEntry{}
	}
	return out, rows.Err()
}

// DeleteIPAllowlist removes one entry by id.
func (s *Store) DeleteIPAllowlist(id int64) error {
	_, err := s.keysDB().Exec(`DELETE FROM ip_allowlist WHERE id=?`, id)
	s.allow.invalidate()
	return err
}

// allowlistCacheTTL bounds how long a snapshot may outlive an edit made by
// another process (the list lives in the machine-global keys database). Writes
// through this Store invalidate immediately.
const allowlistCacheTTL = 2 * time.Second

// allowlistCache is a copy-on-write snapshot of the parsed allowlist.
type allowlistCache struct {
	mu         sync.RWMutex
	loaded     bool
	at         time.Time
	gen        uint64 // bumped by invalidate so a slow reload never overwrites newer state
	nets       []*net.IPNet
	ips        []net.IP
	refreshing atomic.Bool
	now        func() time.Time // test hook; nil means time.Now
}

func (c *allowlistCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// invalidate drops the snapshot; the next match reloads it synchronously.
func (c *allowlistCache) invalidate() {
	c.mu.Lock()
	c.loaded = false
	c.gen++
	c.mu.Unlock()
}

// reloadAllowlist reads the table once and publishes a new snapshot stamped at.
// On a read error the previous snapshot is kept (fail static) and its age reset so
// a broken database is not hammered on every request.
func (s *Store) reloadAllowlist(at time.Time) {
	c := &s.allow
	c.mu.RLock()
	gen := c.gen
	c.mu.RUnlock()
	entries, err := s.ListIPAllowlist()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen {
		return // a write invalidated the cache while we were reading
	}
	if err != nil {
		if c.loaded {
			c.at = at
		}
		return
	}
	var nets []*net.IPNet
	var ips []net.IP
	for _, e := range entries {
		if strings.Contains(e.CIDR, "/") {
			if _, n, perr := net.ParseCIDR(e.CIDR); perr == nil {
				nets = append(nets, n)
			}
		} else if ip := net.ParseIP(e.CIDR); ip != nil {
			ips = append(ips, ip)
		}
	}
	c.nets, c.ips, c.loaded, c.at = nets, ips, true, at
}

// AllowlistMatch reports whether ip (host form, no port) matches any allowlist
// entry. It is served from an in-memory snapshot: the first call (or the first
// after a write through this Store) loads it, and an expired snapshot is
// refreshed in the background while the previous one keeps answering, so the
// forwarding hot path never waits on SQLite.
func (s *Store) AllowlistMatch(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	c := &s.allow
	now := c.clock()
	c.mu.RLock()
	loaded, at := c.loaded, c.at
	c.mu.RUnlock()
	if !loaded {
		s.reloadAllowlist(now)
	} else if now.Sub(at) > allowlistCacheTTL && c.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer c.refreshing.Store(false)
			s.reloadAllowlist(now)
		}()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, other := range c.ips {
		if other.Equal(ip) {
			return true
		}
	}
	return false
}
