package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MergeStats reports what a union merge added vs. skipped as already-present.
type MergeStats struct {
	FlowsAdded      int `json:"flowsAdded"`
	FlowsSkipped    int `json:"flowsSkipped"`
	FindingsAdded   int `json:"findingsAdded"`
	FindingsSkipped int `json:"findingsSkipped"`
	BodiesAdded     int `json:"bodiesAdded"`
}

// MergeFrom unions another project's flows and findings into this one (additive,
// non-destructive) — the "pull" of a git-like collaboration. Content-addressed
// bodies dedupe automatically; flows dedupe by a content signature (so re-merging
// the same peer is idempotent); findings are appended (remapping their PoC flow
// references to the new local flow ids) and deduped by a title/target signature.
// A "peer/<label>" tag is added to every imported flow for provenance.
//
// peerDBPath is a peer project's interceptor.db (opened read-only); peerBodiesDir
// is its bodies/ directory (may be absent for an empty project).
func (s *Store) MergeFrom(peerDBPath, peerBodiesDir, label string) (MergeStats, error) {
	return s.mergeFrom(peerDBPath, peerBodiesDir, label, mergeHooks{})
}

type mergeHooks struct {
	afterBodiesPublished func()
}

// queryPeerFindings keeps project merge compatible with databases created
// before the additive evidence-first envelope columns existed.
func queryPeerFindings(peer *sql.DB) (*sql.Rows, error) {
	columns, err := peerTableColumns(peer, "findings")
	if err != nil {
		return nil, err
	}
	optional := func(name string) string {
		if columns[name] {
			return name
		}
		return `''`
	}
	query := fmt.Sprintf(`SELECT id, severity, status, source, title, target, detail,
		evidence, fix, %s, %s, %s, %s, %s, %s, %s,
		%s, %s, %s, %s, %s FROM findings`, optional("body"), optional("impact"), optional("why"),
		optional("cwe"), optional("environment"), optional("cvss"), optional("verification_instructions"),
		optional("summary"), optional("confidence"), optional("retest"), optional("targets"), optional("proof_review"))
	return peer.Query(query)
}

// peerTableColumns reads a table's schema without assuming that the peer was
// created by the current binary. Merge is intentionally tolerant of old
// findings tables that predate the evidence-first envelope columns.
func peerTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func peerHasTable(db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name=?`, table).Scan(&count)
	return count > 0, err
}

func (s *Store) mergeFrom(peerDBPath, peerBodiesDir, label string, hooks mergeHooks) (MergeStats, error) {
	var stats MergeStats

	peer, err := sql.Open("sqlite", "file:"+peerDBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return stats, fmt.Errorf("open peer db: %w", err)
	}
	defer peer.Close()
	if err := preflightPeerFindings(peer); err != nil {
		return stats, err
	}

	// 1. Copy peer bodies (content-addressed → dedup by filename/hash).
	if peerBodiesDir != "" {
		added, release, err := s.copyBodies(peerBodiesDir)
		if err != nil {
			return stats, err
		}
		defer release()
		stats.BodiesAdded = added
		if hooks.afterBodiesPublished != nil {
			hooks.afterBodiesPublished()
		}
	}
	if err := s.validatePeerFindingImages(peer); err != nil {
		return stats, err
	}

	// 2. Union flows. Build my existing signature set, then insert unseen peer flows.
	seenFlows, err := s.flowSignatures(s.db)
	if err != nil {
		return stats, fmt.Errorf("index local flows: %w", err)
	}
	peerToLocal := map[int64]int64{}
	rows, err := peer.Query(`SELECT id, ts, method, scheme, host, port, path, http_version, status,
		req_headers, res_headers, req_body_hash, res_body_hash, req_len, res_len, mime,
		duration_ms, client_addr, error, flags, note FROM flows`)
	if err != nil {
		return stats, fmt.Errorf("read peer flows: %w", err)
	}
	type pflow struct {
		f    Flow
		peer int64
		note string
	}
	var pending []pflow
	for rows.Next() {
		var f Flow
		var tsMs int64
		var reqH, resH, note string
		if err := rows.Scan(&f.ID, &tsMs, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path,
			&f.HTTPVersion, &f.Status, &reqH, &resH, &f.ReqBodyHash, &f.ResBodyHash,
			&f.ReqLen, &f.ResLen, &f.Mime, &f.DurationMs, &f.ClientAddr, &f.Error, &f.Flags, &note); err != nil {
			rows.Close()
			return stats, err
		}
		f.TS = time.UnixMilli(tsMs)
		_ = json.Unmarshal([]byte(reqH), &f.ReqHeaders)
		_ = json.Unmarshal([]byte(resH), &f.ResHeaders)
		pending = append(pending, pflow{f: f, peer: f.ID, note: note})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return stats, fmt.Errorf("iterate peer flows: %w", err)
	}
	rows.Close()
	for _, pf := range pending {
		for _, bodyHash := range []string{pf.f.ReqBodyHash, pf.f.ResBodyHash} {
			if bodyHash == "" {
				continue
			}
			if !isContentHash(bodyHash) {
				return stats, fmt.Errorf("invalid body hash %q referenced by peer flow %d", bodyHash, pf.peer)
			}
			if info, err := os.Stat(s.bodyPath(bodyHash)); err != nil || !info.Mode().IsRegular() {
				return stats, fmt.Errorf("missing body %s referenced by peer flow %d", bodyHash, pf.peer)
			}
		}
	}

	tag := "peer/" + sanitizeLabel(label)
	for _, pf := range pending {
		sig := flowSig(pf.f)
		if local, ok := seenFlows[sig]; ok {
			peerToLocal[pf.peer] = local
			stats.FlowsSkipped++
			continue
		}
		f := pf.f
		f.ID = 0
		f.Note = pf.note
		newID, err := s.insertFlow(&f, []string{tag})
		if err != nil {
			return stats, fmt.Errorf("insert merged flow: %w", err)
		}
		peerToLocal[pf.peer] = newID
		seenFlows[sig] = newID
		stats.FlowsAdded++
	}

	// 3. Union findings (append + remap PoC flow references, dedupe by signature).
	seenFindings, err := s.findingSignatures(s.db)
	if err != nil {
		return stats, fmt.Errorf("index local findings: %w", err)
	}
	frows, err := queryPeerFindings(peer)
	if err != nil {
		return stats, fmt.Errorf("read peer findings: %w", err)
	}
	type pfind struct {
		f      Finding
		peerID int64
	}
	var pendingF []pfind
	for frows.Next() {
		var f Finding
		if err := frows.Scan(&f.ID, &f.Severity, &f.Status, &f.Source, &f.Title, &f.Target,
			&f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Environment, &f.Cvss, &f.VerificationInstructions,
			&f.Summary, &f.Confidence, &f.Retest, &f.Targets, &f.ProofReview); err != nil {
			frows.Close()
			return stats, err
		}
		pendingF = append(pendingF, pfind{f: f, peerID: f.ID})
	}
	if err := frows.Err(); err != nil {
		frows.Close()
		return stats, fmt.Errorf("iterate peer findings: %w", err)
	}
	frows.Close()
	hasFindingFlows, err := peerHasTable(peer, "finding_flows")
	if err != nil {
		return stats, fmt.Errorf("check peer finding flows: %w", err)
	}
	hasFindingTags, err := peerHasTable(peer, "finding_tags")
	if err != nil {
		return stats, fmt.Errorf("check peer finding tags: %w", err)
	}

	for _, pf := range pendingF {
		sig := findingSig(pf.f)
		if seenFindings[sig] {
			stats.FindingsSkipped++
			continue
		}
		f := pf.f
		f.ID = 0
		// Legacy findings may not have a body column. Seed the canonical body
		// from their old detail/evidence fields before folding in table-only
		// finding_flows, otherwise the first flow would silently hide the text.
		if f.Body == "" {
			f.Body = initialBody(f.Detail, f.Evidence)
		}
		// Fold legacy/table-only PoC attachments into the body before creation;
		// CreateFinding persists the row and body-derived attachments together.
		var ffrows *sql.Rows
		if hasFindingFlows {
			ffrows, err = peer.Query(`SELECT flow_id, ord, note FROM finding_flows WHERE finding_id=? ORDER BY ord`, pf.peerID)
			if err != nil {
				return stats, fmt.Errorf("read peer finding flows: %w", err)
			}
		}
		for ffrows != nil && ffrows.Next() {
			var peerFlowID int64
			var ord int
			var note string
			if err := ffrows.Scan(&peerFlowID, &ord, &note); err != nil {
				ffrows.Close()
				return stats, fmt.Errorf("scan peer finding flow: %w", err)
			}
			// Keep the peer id until all legacy attachments have been folded into
			// the body. This lets the split below distinguish an unmapped/purged
			// peer flow from a coincidentally equal local id.
			f.Body = insertFlowIntoBody(f.Body, peerFlowID, note, -1)
		}
		if ffrows != nil {
			if err := ffrows.Err(); err != nil {
				ffrows.Close()
				return stats, fmt.Errorf("iterate peer finding flows: %w", err)
			}
			ffrows.Close()
		}

		// Copy finding tags (same slug model as flow tags). The table was added
		// after the original findings schema, so its absence is a valid legacy
		// project rather than a merge failure.
		var trows *sql.Rows
		if hasFindingTags {
			trows, err = peer.Query(`SELECT tag FROM finding_tags WHERE finding_id=?`, pf.peerID)
			if err != nil {
				return stats, fmt.Errorf("read peer finding tags: %w", err)
			}
		}
		for trows != nil && trows.Next() {
			var tag string
			if err := trows.Scan(&tag); err != nil {
				trows.Close()
				return stats, fmt.Errorf("scan peer finding tag: %w", err)
			}
			if tag != "" {
				f.Tags = append(f.Tags, tag)
			}
		}
		if trows != nil {
			if err := trows.Err(); err != nil {
				trows.Close()
				return stats, fmt.Errorf("iterate peer finding tags: %w", err)
			}
			trows.Close()
		}

		remapFindingTargets(&f, peerToLocal)
		f.Body = remapBodyFlowIDs(markMissingMergedFlowBlocks(f.Body, peerToLocal), peerToLocal)
		_, err = s.CreateFinding(&f)
		if err != nil {
			return stats, fmt.Errorf("insert merged finding: %w", err)
		}
		seenFindings[sig] = true
		stats.FindingsAdded++
	}

	return stats, nil
}

// preflightPeerFindings folds legacy table-only attachments into every peer
// finding and validates a worst-case serialized body before any local body or
// database mutation. Flow IDs are widened to the longest possible int64 and all
// flow blocks carry the missing marker, so later remapping cannot make the real
// merged body larger than the preflight candidate.
func preflightPeerFindings(peer *sql.DB) error {
	hasFindingFlows, err := peerHasTable(peer, "finding_flows")
	if err != nil {
		return fmt.Errorf("check peer finding flows: %w", err)
	}
	rows, err := queryPeerFindings(peer)
	if err != nil {
		return fmt.Errorf("read peer findings for preflight: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.Severity, &f.Status, &f.Source, &f.Title, &f.Target,
			&f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Environment,
			&f.Cvss, &f.VerificationInstructions, &f.Summary, &f.Confidence, &f.Retest, &f.Targets, &f.ProofReview); err != nil {
			return err
		}
		if err := validateFindingEnvironment(f.Environment); err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
		if err := normalizeFindingAssessment(&f); err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
		if err := validateFindingNarrativeSize(f); err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
		if err := validateFindingConfidence(f.Confidence); err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
		if f.Body == "" {
			f.Body = initialBody(f.Detail, f.Evidence)
			if err := validateFindingBodySize(f.Body); err != nil {
				return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
			}
		}
		if hasFindingFlows {
			flowRows, err := peer.Query(`SELECT flow_id, ord, note FROM finding_flows WHERE finding_id=? ORDER BY ord`, f.ID)
			if err != nil {
				return fmt.Errorf("read peer finding %d flows for preflight: %w", f.ID, err)
			}
			for flowRows.Next() {
				var flowID int64
				var ord int
				var note string
				if err := flowRows.Scan(&flowID, &ord, &note); err != nil {
					flowRows.Close()
					return err
				}
				f.Body = insertFlowIntoBody(f.Body, flowID, note, -1)
				if err := validateFindingBodySize(f.Body); err != nil {
					flowRows.Close()
					return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
				}
			}
			if err := flowRows.Err(); err != nil {
				flowRows.Close()
				return fmt.Errorf("iterate peer finding %d flows for preflight: %w", f.ID, err)
			}
			flowRows.Close()
		}
		normalized, err := NormalizeFindingBody(f.Body)
		if err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
		f.Body = worstCaseMergedBody(normalized)
		if err := validateFindingNarrativeSize(f); err != nil {
			return fmt.Errorf("preflight peer finding %d: %w", f.ID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate peer findings for preflight: %w", err)
	}
	return nil
}

func worstCaseMergedBody(body string) string {
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return body
	}
	for i := range recs {
		if recs[i].FlowID > 0 {
			recs[i].FlowID = math.MaxInt64
		}
		if recs[i].SourceFlowID > 0 {
			recs[i].SourceFlowID = math.MaxInt64
		}
		if recs[i].Type == "flow" {
			recs[i].Missing = true
		}
	}
	encoded, _ := json.Marshal(recs)
	return string(encoded)
}

func markMissingMergedFlowBlocks(body string, peerToLocal map[int64]int64) string {
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return body
	}
	for i := range recs {
		if recs[i].Type == "flow" && recs[i].FlowID > 0 {
			if _, ok := peerToLocal[recs[i].FlowID]; !ok {
				recs[i].Missing = true
			}
		}
	}
	encoded, _ := json.Marshal(recs)
	return string(encoded)
}

func (s *Store) validatePeerFindingImages(peer *sql.DB) error {
	columns, err := peerTableColumns(peer, "findings")
	if err != nil {
		return fmt.Errorf("inspect peer findings: %w", err)
	}
	// The body column was added after the original findings schema. A peer
	// without it can still contribute its row and finding_flows attachments;
	// there are simply no inline image references to validate.
	if !columns["body"] {
		return nil
	}
	rows, err := peer.Query(`SELECT id, body FROM findings WHERE body != ''`)
	if err != nil {
		return fmt.Errorf("read peer finding images: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var findingID int64
		var bodyJSON string
		if err := rows.Scan(&findingID, &bodyJSON); err != nil {
			return err
		}
		if err := validateFindingBodySize(bodyJSON); err != nil {
			return fmt.Errorf("invalid body referenced by peer finding %d: %w", findingID, err)
		}
		normalized, err := NormalizeFindingBody(bodyJSON)
		if err != nil {
			return fmt.Errorf("invalid body referenced by peer finding %d: %w", findingID, err)
		}
		var blocks []blockRecord
		if err := json.Unmarshal([]byte(normalized), &blocks); err != nil {
			return fmt.Errorf("decode peer finding %d body: %w", findingID, err)
		}
		if err := validateFindingBodySize(normalized); err != nil {
			return fmt.Errorf("invalid body referenced by peer finding %d: %w", findingID, err)
		}
		for _, block := range blocks {
			if block.Type != "image" {
				continue
			}
			if !isContentHash(block.Hash) {
				return fmt.Errorf("invalid image body hash %q referenced by peer finding %d", block.Hash, findingID)
			}
			if info, err := os.Stat(s.bodyPath(block.Hash)); err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("missing image body %s referenced by peer finding %d", block.Hash, findingID)
			}
		}
	}
	return rows.Err()
}

// copyBodies copies content-addressed body blobs from a peer bodies dir into this
// store's bodies dir, skipping any already present. Each blob must use the
// canonical <2>/<2>/<hash> layout and its streamed SHA-256 must match its name.
func (s *Store) copyBodies(peerBodiesDir string) (int, func(), error) {
	return s.copyBodiesWithOps(peerBodiesDir, bodyPublishOps{rename: os.Rename})
}

type bodyPublishOps struct {
	beforeRename func(dst string)
	rename       func(oldPath, newPath string) error
}

func (s *Store) copyBodiesWithOps(peerBodiesDir string, ops bodyPublishOps) (int, func(), error) {
	type stagedBody struct {
		tmp string
		dst string
	}
	var staged []stagedBody
	var verifiedHashes []string
	cleanup := func() {
		for _, body := range staged {
			_ = os.Remove(body.tmp)
		}
	}
	err := filepath.WalkDir(peerBodiesDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".tmp-") {
			return nil
		}
		if !isContentHash(name) {
			return fmt.Errorf("invalid body archive entry %q", p)
		}
		rel, err := filepath.Rel(peerBodiesDir, p)
		if err != nil || filepath.Clean(rel) != filepath.Join(name[:2], name[2:4], name) {
			return fmt.Errorf("invalid body archive layout %q", rel)
		}
		dst := s.bodyPath(name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp, actual, err := copyBodyToTemp(p, filepath.Dir(dst))
		if err != nil {
			return err
		}
		if actual != name {
			_ = os.Remove(tmp)
			return fmt.Errorf("body hash mismatch for %s: got %s", name, actual)
		}
		if _, err := os.Stat(dst); err == nil {
			_ = os.Remove(tmp)
			verifiedHashes = append(verifiedHashes, name)
			return nil
		}
		staged = append(staged, stagedBody{tmp: tmp, dst: dst})
		verifiedHashes = append(verifiedHashes, name)
		return nil
	})
	if err != nil {
		cleanup()
		return 0, func() {}, err
	}
	release := s.protectMergeBodies(verifiedHashes)
	added := 0
	for i, body := range staged {
		if _, err := os.Stat(body.dst); err == nil {
			_ = os.Remove(body.tmp)
			continue
		}
		if ops.beforeRename != nil {
			ops.beforeRename(body.dst)
		}
		if err := ops.rename(body.tmp, body.dst); err != nil {
			expected := filepath.Base(body.dst)
			actual, verifyErr := bodyFileDigest(body.dst)
			if verifyErr == nil && actual == expected {
				_ = os.Remove(body.tmp)
				continue
			}
			for _, rest := range staged[i:] {
				_ = os.Remove(rest.tmp)
			}
			release()
			if verifyErr == nil {
				return added, func() {}, fmt.Errorf("destination body hash mismatch for %s: got %s", expected, actual)
			}
			return added, func() {}, err
		}
		added++
	}
	return added, release, nil
}

func bodyFileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyBodyToTemp(src, dstDir string) (tmpPath, hash string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dstDir, ".tmp-merge-*")
	if err != nil {
		return "", "", err
	}
	tmpPath = out.Name()
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(tmpPath)
		}
	}()
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", "", err
	}
	if err = out.Close(); err != nil {
		return "", "", err
	}
	return tmpPath, hex.EncodeToString(h.Sum(nil)), nil
}

// flowSignatures returns a map of content-signature → flow id for every flow in db.
func (s *Store) flowSignatures(db *sql.DB) (map[string]int64, error) {
	rows, err := db.Query(`SELECT id, ts, method, scheme, host, port, path, status,
		req_body_hash, res_body_hash, req_len, res_len FROM flows`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var f Flow
		var tsMs int64
		if err := rows.Scan(&f.ID, &tsMs, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path,
			&f.Status, &f.ReqBodyHash, &f.ResBodyHash, &f.ReqLen, &f.ResLen); err != nil {
			return nil, err
		}
		f.TS = time.UnixMilli(tsMs)
		out[flowSig(f)] = f.ID
	}
	return out, rows.Err()
}

// flowSig is the content signature used for idempotent flow dedup on merge. It
// covers the immutable request identity plus response-side content hashes, so the
// same captured exchange collapses across instances but genuinely distinct replays
// (different ts / bodies) stay separate.
func flowSig(f Flow) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n%d\n%s\n%d\n%s\n%s\n%d\n%d\n%d",
		f.Method, f.Scheme, f.Host, f.Port, f.Path, f.TS.UnixMilli(),
		f.ReqBodyHash, f.ResBodyHash, f.Status, f.ReqLen, f.ResLen)
	return hex.EncodeToString(h.Sum(nil))
}

// findingSignatures returns the set of finding signatures already present in db.
func (s *Store) findingSignatures(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`SELECT title, target, severity, source, detail, targets FROM findings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.Title, &f.Target, &f.Severity, &f.Source, &f.Detail, &f.Targets); err != nil {
			return nil, err
		}
		out[findingSig(f)] = true
	}
	return out, rows.Err()
}

// findingSig is the dedup signature for findings on merge.
func findingSig(f Finding) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%s", f.Title, f.Target, f.Severity, f.Source, f.Detail)
	h.Write(findingTargetsSignature(f.Targets))
	return hex.EncodeToString(h.Sum(nil))
}

// remapBodyFlowIDs rewrites every flowId inside a finding's stored body JSON
// through the peer→local id map. Unmapped references are left as-is (they render
// as "missing" — the block + note are preserved).
func remapBodyFlowIDs(body string, m map[int64]int64) string {
	if body == "" {
		return body
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return body
	}
	for i := range recs {
		if recs[i].FlowID != 0 {
			if local, ok := m[recs[i].FlowID]; ok {
				recs[i].FlowID = local
			}
		}
		if recs[i].SourceFlowID != 0 {
			if local, ok := m[recs[i].SourceFlowID]; ok {
				recs[i].SourceFlowID = local
			} else {
				recs[i].SourceFlowID = 0
			}
		}
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

// sanitizeLabel makes a peer label safe for use in a tag/title (alnum, dash, dot).
func sanitizeLabel(label string) string {
	if label == "" {
		return "peer"
	}
	out := make([]rune, 0, len(label))
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "peer"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}
