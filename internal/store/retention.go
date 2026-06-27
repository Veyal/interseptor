package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostStat aggregates flow counts and approximate byte totals for one host.
// Bytes is SUM(req_len + res_len) across all flows for that host — an
// approximation, because content-addressed bodies are deduplicated on disk, so
// flows sharing a body each contribute to the sum even though only one file
// exists. Use it for a rough UI size-breakdown, not an exact disk-usage figure.
type HostStat struct {
	Host  string
	Flows int64
	Bytes int64
}

// retentionHostMatches is a local copy of the wildcard host-matching logic in
// internal/scope (which we cannot import — it depends on this package). Behavior
// is identical: "" matches any host; "*.acme.com" matches "acme.com" and every
// subdomain; otherwise an exact case-insensitive comparison.
func retentionHostMatches(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if strings.HasPrefix(pattern, "*.") {
		base := pattern[2:]
		return host == base || strings.HasSuffix(host, pattern[1:])
	}
	return host == pattern
}

// DeleteFlowsByHost removes flows by host pattern and returns how many rows
// were deleted.
//
//   - When keepOnly is false, every flow whose host matches ANY pattern in
//     hosts is deleted. An empty hosts slice is a no-op (returns 0, nil).
//   - When keepOnly is true, every flow whose host matches NONE of the
//     patterns is deleted (i.e. the listed hosts are kept, everything else is
//     purged). An empty hosts slice with keepOnly=true is rejected with an
//     error to prevent silently wiping all data.
//
// Pattern matching is case-insensitive and supports leading-wildcard patterns
// (e.g. "*.example.com" matches "example.com" and all subdomains).
func (s *Store) DeleteFlowsByHost(hosts []string, keepOnly bool) (int64, error) {
	if len(hosts) == 0 {
		if keepOnly {
			return 0, errors.New("store.DeleteFlowsByHost: keepOnly requires at least one host pattern")
		}
		return 0, nil
	}

	// Load all distinct hosts from the flows table so we can apply wildcard
	// matching in Go (SQLite has no native wildcard-host function). We only
	// fetch the host column — the result set is at most as large as the number
	// of distinct hosts in history, which is small compared to the flow count.
	rows, err := s.db.Query(`SELECT DISTINCT lower(host) FROM flows`)
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: list hosts: %w", err)
	}
	var allHosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store.DeleteFlowsByHost: scan host: %w", err)
		}
		allHosts = append(allHosts, h)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: close rows: %w", err)
	}

	// Determine which stored hosts satisfy the pattern list.
	matchesAny := func(storedHost string) bool {
		for _, p := range hosts {
			if retentionHostMatches(p, storedHost) {
				return true
			}
		}
		return false
	}

	// Build the set of stored hosts that should be deleted.
	var toDelete []string
	for _, h := range allHosts {
		matched := matchesAny(h)
		if keepOnly {
			// keepOnly: delete hosts that do NOT match any keep pattern.
			if !matched {
				toDelete = append(toDelete, h)
			}
		} else {
			// normal: delete hosts that DO match.
			if matched {
				toDelete = append(toDelete, h)
			}
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	// DELETE FROM flows WHERE lower(host) IN (?, ?, …)
	args := make([]any, len(toDelete))
	for i, h := range toDelete {
		args[i] = h
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(toDelete)), ",")
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: begin: %w", err)
	}
	defer tx.Rollback()
	// FTS rows are keyed by rowid (= flow id); delete them for the matching hosts
	// in one statement (the indexed content columns aren't needed to delete), then
	// delete the flows — both in one transaction so search can't see orphans.
	if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (SELECT id FROM flows WHERE lower(host) IN (`+ph+`))`, args...); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: unindex: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM flows WHERE lower(host) IN (`+ph+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: commit: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GCBodies reclaims body files that are no longer referenced by any flow.
// It walks bodiesDir, collects every file whose name looks like a content hash
// (a 64-hex-char sha256 stored under a two-level prefix directory), queries the
// flows table for all referenced hashes, and removes any file whose hash is not
// among them.
//
// GCBodies returns the number of files removed and the total bytes freed.
// It is safe to call while the store is in use: it never removes a file that
// is still referenced, and it never touches files outside bodiesDir or files
// whose names do not match the content-hash scheme (e.g. ".tmp-*" partials).
func (s *Store) GCBodies() (removedFiles int64, freedBytes int64, err error) {
	// 1. Collect every hash referenced by at least one flow.
	referenced := make(map[string]struct{})
	rows, err := s.db.Query(
		`SELECT h FROM (
			SELECT req_body_hash AS h FROM flows WHERE req_body_hash != ''
			UNION
			SELECT res_body_hash AS h FROM flows WHERE res_body_hash != ''
		)`)
	if err != nil {
		return 0, 0, fmt.Errorf("store.GCBodies: query refs: %w", err)
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("store.GCBodies: scan ref: %w", err)
		}
		if h != "" {
			referenced[h] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return 0, 0, fmt.Errorf("store.GCBodies: close rows: %w", err)
	}

	// 2. Walk bodiesDir and remove any content-hash file that is not referenced.
	// Body files live at bodiesDir/<2-hex>/<2-hex>/<64-hex-hash>.
	// We only remove files at depth-2 subdirectories whose name is exactly
	// the 64-char sha256 hex string — skipping ".tmp-*" partials and anything
	// else that doesn't match.
	const sha256HexLen = 64
	err = filepath.WalkDir(s.bodiesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip entries we can't stat rather than aborting the whole GC.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		// Only operate on files whose name is a full sha256 hex string.
		if len(name) != sha256HexLen || !isHex(name) {
			return nil
		}
		if _, kept := referenced[name]; kept {
			return nil
		}
		// Unreferenced — measure size then remove.
		info, err := d.Info()
		if err != nil {
			return nil // best-effort; skip if we can't stat
		}
		sz := info.Size()
		if err := os.Remove(path); err != nil {
			return nil // best-effort; don't abort walk on permission errors
		}
		removedFiles++
		freedBytes += sz
		return nil
	})
	if err != nil {
		return removedFiles, freedBytes, fmt.Errorf("store.GCBodies: walk: %w", err)
	}
	return removedFiles, freedBytes, nil
}

// isHex reports whether s consists entirely of lowercase or uppercase hex digits.
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// HostStats returns per-host flow counts and approximate byte totals, sorted
// descending by bytes. See HostStat for the approximation caveat.
func (s *Store) HostStats() ([]HostStat, error) {
	rows, err := s.db.Query(
		`SELECT host, COUNT(*) AS flows, SUM(req_len + res_len) AS bytes
		 FROM flows
		 GROUP BY host
		 ORDER BY bytes DESC`)
	if err != nil {
		return nil, fmt.Errorf("store.HostStats: query: %w", err)
	}
	defer rows.Close()
	var out []HostStat
	for rows.Next() {
		var hs HostStat
		if err := rows.Scan(&hs.Host, &hs.Flows, &hs.Bytes); err != nil {
			return nil, fmt.Errorf("store.HostStats: scan: %w", err)
		}
		out = append(out, hs)
	}
	return out, rows.Err()
}
