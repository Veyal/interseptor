package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// ErrBlankHostPattern rejects an empty/whitespace host pattern in an explicit
// purge: match-all is never what a blank list entry means.
var ErrBlankHostPattern = errors.New("store.DeleteFlowsByHost: empty host pattern")

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
	// MatchHost("") is the documented match-all pattern (scope relies on it), so
	// a blank entry here would mean "every host": with keepOnly=false that wipes
	// the whole history from one blank line in a pasted list. Reject it instead
	// of interpreting it.
	for i, h := range hosts {
		if strings.TrimSpace(h) == "" {
			return 0, fmt.Errorf("%w (entry %d)", ErrBlankHostPattern, i+1)
		}
	}
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("store.DeleteFlowsByHost: list hosts: %w", err)
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

// keepNewestDeleteSQL removes every unreferenced flow below an id cutoff.
const keepNewestDeleteSQL = `DELETE FROM flows WHERE id < ? AND id NOT IN (` + referencedFlowIDsSQL + `)`

// DeleteFlowsKeepNewest keeps only the most-recent n flows (by id, descending)
// and deletes the rest, preserving flows findings reference (referencedFlowIDsSQL). n<=0 deletes nothing.
//
// The rows to delete are addressed by an id cutoff — the id of the n-th newest
// flow. The earlier form, id NOT IN (the n newest ids), tested every flow in
// the table against that list four times over, and did so on every retention
// tick even when nothing was over the limit: half a second of write lock on
// 100,000 flows, during which capture could not commit.
func (s *Store) DeleteFlowsKeepNewest(n int64) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	s.syncFlows()
	const cutoffSQL = `SELECT id FROM flows ORDER BY id DESC LIMIT 1 OFFSET ?`
	var cutoff int64
	// Checked before taking the write lock: the common case is a history that
	// is still under the limit.
	if err := s.db.QueryRow(cutoffSQL, n-1).Scan(&cutoff); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: cutoff: %w", err)
	}
	_ = s.FlushWSFrames() // queued frames must be visible to the delete below
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: begin: %w", err)
	}
	defer tx.Rollback()
	// Re-read under the write lock so the cutoff matches the rows being deleted.
	if err := tx.QueryRow(cutoffSQL, n-1).Scan(&cutoff); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: cutoff: %w", err)
	}
	const doomed = `SELECT id FROM flows WHERE id < ? AND id NOT IN (` + referencedFlowIDsSQL + `)`
	if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (`+doomed+`)`, cutoff); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: unindex: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (`+doomed+`)`, cutoff); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: untag: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ws_frames WHERE flow_id IN (`+doomed+`)`, cutoff); err != nil {
		return 0, fmt.Errorf("store.DeleteFlowsKeepNewest: ws frames: %w", err)
	}
	res, err := tx.Exec(keepNewestDeleteSQL, cutoff)
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
	// One pass over the table, deduplicated here. The four-way UNION this
	// replaces read the table four times and built a temp b-tree of every
	// hash to deduplicate them. Nearly every hash read has been seen already;
	// RawBytes scans it into a reused buffer and the map lookup keyed by
	// string(h) does not copy, so only a new hash is retained.
	rows, err := s.db.Query(
		`SELECT req_body_hash, res_body_hash, original_req_body_hash, original_res_body_hash
		   FROM flows
		  WHERE req_body_hash != '' OR res_body_hash != ''
		     OR original_req_body_hash != '' OR original_res_body_hash != ''`)
	if err != nil {
		return nil, fmt.Errorf("store.GCBodies: query refs: %w", err)
	}
	var cols [4]sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&cols[0], &cols[1], &cols[2], &cols[3]); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store.GCBodies: scan ref: %w", err)
		}
		for _, h := range cols {
			if len(h) == 0 {
				continue // NULL or ''
			}
			if _, seen := referenced[string(h)]; !seen {
				referenced[string(h)] = struct{}{}
			}
		}
	}
	// A reference set cut short by a read error would make GC delete bodies
	// that flows still point at; rows.Close alone does not report it.
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store.GCBodies: read refs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store.GCBodies: close rows: %w", err)
	}
	// A live prune snapshot can be undone, so its bodies must survive the sweep.
	// Fail closed: a sweep that cannot read which bodies an undo still needs
	// must not run, or it deletes exactly those bodies.
	protected, err := s.protectedPruneHashes()
	if err != nil {
		return nil, fmt.Errorf("store.GCBodies: prune snapshot refs: %w", err)
	}
	for h := range protected {
		referenced[h] = struct{}{}
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
// descending by bytes. See HostStat for the approximation caveat. Queued
// write-behind capture is drained first so the breakdown includes the newest
// flows.
func (s *Store) HostStats() ([]HostStat, error) {
	s.syncFlows()
	rows, err := s.db.Query(
		`SELECT host, COUNT(*) AS flows, COALESCE(SUM(COALESCE(req_len,0) + COALESCE(res_len,0)),0) AS bytes
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

// PruneOptions selects which flows one prune removes. Every non-empty filter is
// ANDed with the host selection, so a cleanup can keep the target host and still
// drop its static assets or its 2xx noise.
type PruneOptions struct {
	Hosts        []string
	KeepOnly     bool
	Methods      []string
	Statuses     []int
	MimePrefixes []string
	PathPrefixes []string
	// DryRun reports what would go without deleting anything.
	DryRun bool
	// UndoWindow keeps the removed rows restorable for this long (default
	// DefaultPruneUndoWindow).
	UndoWindow time.Duration
}

// Dir reports whether the prune removes matching rows (delete) or everything
// that does not match (keepOnly).
func (o PruneOptions) Dir() string {
	if o.KeepOnly {
		return "keepOnly"
	}
	return "delete"
}

// DefaultPruneUndoWindow is how long a prune stays reversible.
const DefaultPruneUndoWindow = 30 * time.Minute

// PruneHostStat is one host's share of a prune.
type PruneHostStat struct {
	Host  string `json:"host"`
	Flows int64  `json:"flows"`
	Bytes int64  `json:"bytes"`
}

// PruneResult is what a prune (or its dry run) would remove and what it did.
type PruneResult struct {
	DryRun  bool            `json:"dryRun"`
	Mode    string          `json:"mode"`
	Matched int64           `json:"matched"`
	Bytes   int64           `json:"bytes"`
	PerHost []PruneHostStat `json:"perHost"`
	Deleted int64           `json:"deleted"`
	// PruneID addresses this prune's undo snapshot (0 on a dry run or when
	// nothing matched).
	PruneID int64 `json:"pruneId,omitempty"`
	// UndoExpires is the unix-ms deadline for RestorePrune.
	UndoExpires int64 `json:"undoExpires,omitempty"`
}

// pruneFilterSQL builds the ANDed filter clause for PruneOptions.
func (o PruneOptions) filterSQL() (string, []any) {
	var where []string
	var args []any
	if len(o.Methods) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(o.Methods)), ",")
		where = append(where, "upper(method) IN ("+ph+")")
		for _, m := range o.Methods {
			args = append(args, strings.ToUpper(strings.TrimSpace(m)))
		}
	}
	if len(o.Statuses) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(o.Statuses)), ",")
		where = append(where, "status IN ("+ph+")")
		for _, st := range o.Statuses {
			args = append(args, st)
		}
	}
	if len(o.MimePrefixes) > 0 {
		var or []string
		for _, p := range o.MimePrefixes {
			or = append(or, "lower(COALESCE(mime,'')) LIKE ?")
			args = append(args, strings.ToLower(strings.TrimSpace(p))+"%")
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if len(o.PathPrefixes) > 0 {
		var or []string
		for _, p := range o.PathPrefixes {
			or = append(or, "lower(COALESCE(path,'')) LIKE ?")
			args = append(args, strings.ToLower(strings.TrimSpace(p))+"%")
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if len(where) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(where, " AND "), args
}

// pruneSnapshotSchema keeps a pruned batch restorable. Body files are not
// copied: content-addressed bodies stay on disk while the snapshot lives,
// because referencedBodyHashes counts prune_body_hashes too.
const pruneSnapshotSchema = `
CREATE TABLE IF NOT EXISTS prune_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  expires_ts INTEGER NOT NULL,
  mode TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  flows TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS prune_body_hashes (
  snapshot_id INTEGER NOT NULL,
  hash TEXT NOT NULL,
  PRIMARY KEY (snapshot_id, hash)
);
`

// PruneFlows removes matching flows (or counts them for a dry run), keeping the
// batch restorable for op.UndoWindow so a mistaken prune does not need a
// full-project import.
func (s *Store) PruneFlows(op PruneOptions) (PruneResult, error) {
	s.syncFlows()
	res := PruneResult{DryRun: op.DryRun, Mode: op.Dir(), PerHost: []PruneHostStat{}}
	for i, h := range op.Hosts {
		if strings.TrimSpace(h) == "" {
			return res, fmt.Errorf("%w (entry %d)", ErrBlankHostPattern, i+1)
		}
	}
	if len(op.Hosts) == 0 && op.KeepOnly {
		return res, errors.New("store.PruneFlows: keepOnly requires at least one host pattern")
	}

	// Resolve the host selection to an explicit host list (wildcards are matched
	// in Go; SQLite has no native wildcard-host function).
	deleteAll := false
	var selected []string
	if len(op.Hosts) > 0 {
		all, err := s.distinctHosts()
		if err != nil {
			return res, err
		}
		for _, h := range all {
			matched := false
			for _, p := range op.Hosts {
				if retentionHostMatches(p, h) {
					matched = true
					break
				}
			}
			if matched != op.KeepOnly {
				selected = append(selected, h)
			}
		}
		if len(selected) == 0 {
			return res, nil
		}
	} else {
		deleteAll = true
	}

	ids, err := s.pruneCandidateIDs(selected, deleteAll, op)
	if err != nil {
		return res, err
	}
	if len(ids) == 0 {
		return res, nil
	}

	// Aggregate what the selection covers, per host.
	rows, err := s.queryRowsByIDs(ids,
		`SELECT lower(host), COUNT(*), COALESCE(SUM(COALESCE(req_len,0)+COALESCE(res_len,0)),0) FROM flows WHERE id IN (%s) GROUP BY lower(host) ORDER BY 2 DESC`)
	if err != nil {
		return res, err
	}
	// queryRowsByIDs runs the aggregate once per IN-list chunk, so a host
	// whose flows span chunks comes back once per chunk: merge them.
	perHost := map[string]int{}
	for _, row := range rows {
		stat := PruneHostStat{Host: row.str(0), Flows: row.int(1), Bytes: row.int(2)}
		res.Matched += stat.Flows
		res.Bytes += stat.Bytes
		if i, seen := perHost[stat.Host]; seen {
			res.PerHost[i].Flows += stat.Flows
			res.PerHost[i].Bytes += stat.Bytes
			continue
		}
		perHost[stat.Host] = len(res.PerHost)
		res.PerHost = append(res.PerHost, stat)
	}
	sort.SliceStable(res.PerHost, func(i, j int) bool { return res.PerHost[i].Flows > res.PerHost[j].Flows })
	if op.DryRun {
		return res, nil
	}

	window := op.UndoWindow
	if window <= 0 {
		window = DefaultPruneUndoWindow
	}
	expires := time.Now().Add(window).UnixMilli()
	pruneID, err := s.snapshotPrune(ids, op.Dir(), expires)
	if err != nil {
		return res, err
	}
	deleted, err := s.deleteFlowsByIDs(ids)
	if err != nil {
		return res, err
	}
	res.Deleted = deleted
	res.PruneID = pruneID
	res.UndoExpires = expires
	return res, nil
}

// distinctHosts lists the hosts present in history, lowercased.
func (s *Store) distinctHosts() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT lower(host) FROM flows`)
	if err != nil {
		return nil, fmt.Errorf("store: list hosts: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("store: scan host: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// pruneCandidateIDs returns the flow ids a prune removes, chunking the host list
// to stay below SQLite's parameter limit.
func (s *Store) pruneCandidateIDs(selected []string, deleteAll bool, op PruneOptions) ([]int64, error) {
	const hostChunk = 400
	filter, filterArgs := op.filterSQL()
	var ids []int64
	if deleteAll {
		rows, err := s.db.Query(`SELECT id FROM flows WHERE 1=1`+filter, filterArgs...)
		if err != nil {
			return nil, fmt.Errorf("store: prune candidates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	for start := 0; start < len(selected); start += hostChunk {
		end := min(start+hostChunk, len(selected))
		args := make([]any, 0, end-start+len(filterArgs))
		for _, h := range selected[start:end] {
			args = append(args, h)
		}
		args = append(args, filterArgs...)
		ph := strings.TrimRight(strings.Repeat("?,", end-start), ",")
		rows, err := s.db.Query(`SELECT id FROM flows WHERE lower(host) IN (`+ph+`)`+filter, args...)
		if err != nil {
			return nil, fmt.Errorf("store: prune candidates: %w", err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
