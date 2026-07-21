package store

import (
	"fmt"
	"net"
	"strings"
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
		return v4.String(), nil
	}
	return ip.String(), nil
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
	return err
}

// AllowlistMatch reports whether ip (host form, no port) matches any allowlist entry.
func (s *Store) AllowlistMatch(ipStr string) bool {
	ipStr = strings.TrimSpace(ipStr)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	entries, err := s.ListIPAllowlist()
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if matchAllowEntry(ip, e.CIDR) {
			return true
		}
	}
	return false
}

func matchAllowEntry(ip net.IP, cidr string) bool {
	if strings.Contains(cidr, "/") {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return false
		}
		return n.Contains(ip)
	}
	other := net.ParseIP(cidr)
	return other != nil && other.Equal(ip)
}
