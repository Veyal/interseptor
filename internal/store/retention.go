package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/hostpattern"
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

// referencedFlowIDsSQL selects every flow id a finding depends on, so no
// retention pass deletes evidence a finding still points at: body flow blocks
// (finding_flows), proofReview.evidence and capability-claim evidence,
// structured claim evidence, targets[].flow_ids, and the verification baseline
// and payload flows. Malformed JSON columns are read as empty, and hash-only
// references (NULL flow id) are dropped so NOT IN never sees a NULL.
const referencedFlowIDsSQL = `SELECT ref FROM (
	SELECT flow_id AS ref FROM finding_flows
	UNION SELECT baseline_flow FROM finding_verification
	UNION SELECT payload_flow FROM finding_verification
	UNION SELECT json_extract(e.value, '$.flowId') FROM findings f,
		json_each(CASE WHEN json_valid(f.proof_review) THEN f.proof_review ELSE '{}' END, '$.evidence') e
	UNION SELECT json_extract(r.value, '$.flowId') FROM findings f,
		json_each(CASE WHEN json_valid(f.proof_review) THEN f.proof_review ELSE '{}' END, '$.claims') c,
		json_each(c.value, '$.evidence') r
	UNION SELECT json_extract(r.value, '$.flowId') FROM findings f,
		json_each(CASE WHEN json_valid(f.structured) THEN f.structured ELSE '{}' END, '$.claims') c,
		json_each(c.value, '$.evidence') r
	UNION SELECT i.value FROM findings f,
		json_each(CASE WHEN json_valid(f.targets) THEN f.targets ELSE '[]' END) t,
		json_each(t.value, '$.flow_ids') i
) WHERE ref > 0`

func retentionHostMatches(pattern, host string) bool {
	return hostpattern.MatchHost(pattern, host)
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
// (e.g. "*.example.com" matches "example.com" and all subdomains). This is an
// explicit purge: flows findings reference are deleted too, and those findings
// then show the evidence as missing (automatic retention preserves them).
func (s *Store) DeleteFlowsByHost(hosts []string, keepOnly bool) (int64, error) {
	s.syncFlows()
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

	_ = s.FlushWSFrames() // queued frames must be visible to the delete below
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: begin: %w", err)
	}
	defer tx.Rollback()

	// DELETE FROM flows WHERE lower(host) IN (?, ?, …) — chunked to stay below
	// SQLite's 32766 host-parameter limit (a subdomain-heavy capture can hold
	// more distinct hosts than that). All chunks share one transaction.
	const hostChunk = 400
	var deleted int64
	for start := 0; start < len(toDelete); start += hostChunk {
		end := start + hostChunk
		if end > len(toDelete) {
			end = len(toDelete)
		}
		args := make([]any, end-start)
		for i, h := range toDelete[start:end] {
			args[i] = h
		}
		ph := strings.TrimRight(strings.Repeat("?,", end-start), ",")
		// FTS rows are keyed by rowid (= flow id); delete them for the matching
		// hosts (the indexed content columns aren't needed to delete), then the
		// flows — all in one transaction so search can't see orphans.
		hostWhere := `lower(host) IN (` + ph + `)`
		if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (SELECT id FROM flows WHERE `+hostWhere+`)`, args...); err != nil {
			return 0, fmt.Errorf("store.DeleteFlowsByHost: unindex: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (SELECT id FROM flows WHERE `+hostWhere+`)`, args...); err != nil {
			return 0, fmt.Errorf("store.DeleteFlowsByHost: untag: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM ws_frames WHERE flow_id IN (SELECT id FROM flows WHERE `+hostWhere+`)`, args...); err != nil {
			return 0, fmt.Errorf("store.DeleteFlowsByHost: ws frames: %w", err)
		}
		res, err := tx.Exec(`DELETE FROM flows WHERE `+hostWhere, args...)
		if err != nil {
			return 0, fmt.Errorf("store.DeleteFlowsByHost: delete: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += n
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsByHost: commit: %w", err)
	}
	return deleted, nil
}

// DeleteFlowsOlderThan removes every flow whose timestamp (unix milliseconds,
// the unit the flows.ts column stores) is before cutoffMillis, with its FTS
// index and tags, preserving flows findings reference (referencedFlowIDsSQL). Returns the count deleted.
func (s *Store) DeleteFlowsOlderThan(cutoffMillis int64) (int64, error) {
	s.syncFlows()
	_ = s.FlushWSFrames() // queued frames must be visible to the delete below
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (SELECT id FROM flows WHERE ts < ? AND id NOT IN (`+referencedFlowIDsSQL+`))`, cutoffMillis); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: unindex: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (SELECT id FROM flows WHERE ts < ? AND id NOT IN (`+referencedFlowIDsSQL+`))`, cutoffMillis); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: untag: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ws_frames WHERE flow_id IN (SELECT id FROM flows WHERE ts < ? AND id NOT IN (`+referencedFlowIDsSQL+`))`, cutoffMillis); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: ws frames: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM flows WHERE ts < ? AND id NOT IN (`+referencedFlowIDsSQL+`)`, cutoffMillis)
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsOlderThan: commit: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteFlowsKeepNewest keeps only the most-recent n flows (by id, descending)
// and deletes the rest, preserving flows findings reference (referencedFlowIDsSQL). n<=0 deletes nothing.
func (s *Store) DeleteFlowsKeepNewest(n int64) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	s.syncFlows()
	_ = s.FlushWSFrames() // queued frames must be visible to the delete below
	const keepSub = `SELECT id FROM flows ORDER BY id DESC LIMIT ?`
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (SELECT id FROM flows WHERE id NOT IN (`+keepSub+`) AND id NOT IN (`+referencedFlowIDsSQL+`))`, n); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: unindex: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (SELECT id FROM flows WHERE id NOT IN (`+keepSub+`) AND id NOT IN (`+referencedFlowIDsSQL+`))`, n); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: untag: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ws_frames WHERE flow_id IN (SELECT id FROM flows WHERE id NOT IN (`+keepSub+`) AND id NOT IN (`+referencedFlowIDsSQL+`))`, n); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: ws frames: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM flows WHERE id NOT IN (`+keepSub+`) AND id NOT IN (`+referencedFlowIDsSQL+`)`, n)
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: commit: %w", err)
	}
	k, _ := res.RowsAffected()
	return k, nil
}

// GCBodies reclaims body files that are no longer referenced by any flow or
// finding image block. It walks bodiesDir, collects every file whose name looks
// like a content hash (a 64-hex-char sha256 stored under a two-level prefix
// directory), queries flows + finding image hashes, and removes any file whose
// hash is not among them.
//
// GCBodies returns the number of files removed and the total bytes freed. It is
// safe to call while the store is in use: it never removes a file that is still
// referenced, and it never touches files outside bodiesDir or files whose names
// do not match the content-hash scheme (e.g. ".tmp-*" partials).
//
// bodyMu is held only for a map copy and for each individual unlink, never
// across the filesystem walk. On a large engagement that walk takes seconds,
// and the proxy's body capture takes the same lock on every Finalize, so
// holding it across the walk froze capture for the whole pass. Three things
// keep the shorter hold safe:
//
//   - The owned-body snapshot is taken BEFORE the database is queried. A body
//     is published (released from pendingBodies) only after the flow row
//     referencing it has committed, so a hash the snapshot misses had its row
//     committed before the snapshot — and therefore before the query that
//     follows sees the database.
//   - Every hash a writer takes ownership of while the pass runs is recorded,
//     so a body published mid-walk is spared even though neither the snapshot
//     nor the query could have seen it. That includes the awkward case of a
//     capture deduplicating onto a file the walk has already listed.
//   - The decision to delete and the unlink itself happen under one lock hold.
//     A Finalize racing that unlink either already owns the hash, and the file
//     is spared, or finds the file gone and writes it again from its temp file.
//
// A body finalized outside the flow-publication handshake — a HAR import, a
// finding screenshot — is claimed by a pass that is already running, but it
// takes no lasting ownership, so a pass that STARTS between its Finalize and
// the row that references it still sees an orphan. That is why those callers
// write their referencing row promptly, and why PutAndAttachImageRef holds
// bodyMu across both.
func (s *Store) GCBodies() (removedFiles int64, freedBytes int64, err error) {
	// One pass at a time: the pass installs a marker collecting every body
	// claimed while it runs, and that marker needs a single owner.
	s.bodyGCMu.Lock()
	defer s.bodyGCMu.Unlock()

	// 1. Snapshot the bodies writers own now, and start recording the ones they
	// claim from here on. Both under bodyMu, briefly.
	referenced := s.beginBodyGC()
	defer s.endBodyGC()

	// 2. Query the database for referenced hashes, with no lock held.
	stored, err := s.referencedBodyHashes()
	if err != nil {
		return 0, 0, err
	}
	for h := range stored {
		referenced[h] = struct{}{}
	}

	// 3. Walk bodiesDir, with no lock held, listing what looks unreferenced.
	// Body files live at bodiesDir/<2-hex>/<2-hex>/<64-hex-hash>. Only files
	// whose name is exactly the 64-char sha256 hex string are candidates —
	// ".tmp-*" partials and anything else are left alone.
	const sha256HexLen = 64
	type orphan struct {
		path string
		hash string
		size int64
	}
	var orphans []orphan
	err = filepath.WalkDir(s.bodiesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip entries we can't stat rather than aborting the whole GC.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if len(name) != sha256HexLen || !isHex(name) {
			return nil
		}
		if _, kept := referenced[name]; kept {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // best-effort; skip if we can't stat
		}
		orphans = append(orphans, orphan{path: path, hash: name, size: info.Size()})
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("store.GCBodies: walk: %w", err)
	}

	// 4. Remove them, re-checking ownership under the lock one file at a time.
	for _, o := range orphans {
		if s.removeUnownedBody(o.path, o.hash) {
			removedFiles++
			freedBytes += o.size
		}
	}
	return removedFiles, freedBytes, nil
}

// referencedBodyHashes returns every content hash a flow row or a finding image
// block references — the database half of GCBodies' reference set.
func (s *Store) referencedBodyHashes() (map[string]struct{}, error) {
	referenced := make(map[string]struct{})
	rows, err := s.db.Query(
		`SELECT h FROM (
			SELECT req_body_hash AS h FROM flows WHERE req_body_hash != ''
			UNION
			SELECT res_body_hash AS h FROM flows WHERE res_body_hash != ''
			UNION
			SELECT original_req_body_hash AS h FROM flows WHERE original_req_body_hash != ''
			UNION
			SELECT original_res_body_hash AS h FROM flows WHERE original_res_body_hash != ''
		)`)
	if err != nil {
		return nil, fmt.Errorf("store.GCBodies: query refs: %w", err)
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store.GCBodies: scan ref: %w", err)
		}
		if h != "" {
			referenced[h] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store.GCBodies: close rows: %w", err)
	}
	// Finding screenshot/evidence images live in the same bodiesDir; keep them
	// alive even when no flow references the hash.
	imgRefs, err := s.FindingImageHashes()
	if err != nil {
		return nil, fmt.Errorf("store.GCBodies: finding image refs: %w", err)
	}
	for h := range imgRefs {
		referenced[h] = struct{}{}
	}
	return referenced, nil
}

// beginBodyGC snapshots the hashes writers currently own and starts recording
// every hash claimed until endBodyGC. Taking the snapshot before the database
// query is what makes a lock-free walk safe — see GCBodies.
func (s *Store) beginBodyGC() map[string]struct{} {
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	referenced := make(map[string]struct{}, len(s.pendingBodies)+len(s.mergeBodies))
	for h := range s.pendingBodies {
		referenced[h] = struct{}{}
	}
	for h := range s.mergeBodies {
		referenced[h] = struct{}{}
	}
	s.bodyGCClaimed = map[string]struct{}{}
	return referenced
}

func (s *Store) endBodyGC() {
	s.bodyMu.Lock()
	s.bodyGCClaimed = nil
	s.bodyMu.Unlock()
}

// removeUnownedBody unlinks one orphan under bodyMu, after a last check that no
// writer has claimed its hash since the walk listed it. It reports whether the
// file was removed.
func (s *Store) removeUnownedBody(path, hash string) bool {
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	if s.bodyOwnedLocked(hash) {
		return false
	}
	// Best-effort: a permission error must not abort the pass.
	return os.Remove(path) == nil
}

// bodyOwnedLocked reports whether a writer owns hash right now, or claimed it
// at any point during the running GC pass. Callers hold bodyMu.
func (s *Store) bodyOwnedLocked(hash string) bool {
	if s.pendingBodies[hash] > 0 || s.mergeBodies[hash] > 0 {
		return true
	}
	_, claimed := s.bodyGCClaimed[hash]
	return claimed
}

// noteBodyGCClaimLocked records a hash a writer has just taken ownership of, so
// a GC pass already walking cannot reclaim it. The map is nil, and this a
// no-op, whenever no pass is running. Callers hold bodyMu.
func (s *Store) noteBodyGCClaimLocked(hash string) {
	if hash != "" && s.bodyGCClaimed != nil {
		s.bodyGCClaimed[hash] = struct{}{}
	}
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
