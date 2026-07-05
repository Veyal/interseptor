package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	var stats MergeStats

	peer, err := sql.Open("sqlite", "file:"+peerDBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return stats, fmt.Errorf("open peer db: %w", err)
	}
	defer peer.Close()

	// 1. Copy peer bodies (content-addressed → dedup by filename/hash).
	if peerBodiesDir != "" {
		added, err := s.copyBodies(peerBodiesDir)
		if err != nil {
			return stats, err
		}
		stats.BodiesAdded = added
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
	rows.Close()

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
		newID, err := s.InsertFlow(&f)
		if err != nil {
			return stats, fmt.Errorf("insert merged flow: %w", err)
		}
		if pf.note != "" {
			_ = s.SetFlowNote(newID, pf.note)
		}
		_, _ = s.AddFlowTags(newID, []string{tag})
		peerToLocal[pf.peer] = newID
		seenFlows[sig] = newID
		stats.FlowsAdded++
	}

	// 3. Union findings (append + remap PoC flow references, dedupe by signature).
	seenFindings, err := s.findingSignatures(s.db)
	if err != nil {
		return stats, fmt.Errorf("index local findings: %w", err)
	}
	frows, err := peer.Query(`SELECT id, severity, status, source, title, target, detail,
		evidence, fix, body, impact, cvss FROM findings`)
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
			&f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Cvss); err != nil {
			frows.Close()
			return stats, err
		}
		pendingF = append(pendingF, pfind{f: f, peerID: f.ID})
	}
	frows.Close()

	for _, pf := range pendingF {
		sig := findingSig(pf.f)
		if seenFindings[sig] {
			stats.FindingsSkipped++
			continue
		}
		f := pf.f
		f.ID = 0
		f.Body = remapBodyFlowIDs(f.Body, peerToLocal)
		newID, err := s.CreateFinding(&f)
		if err != nil {
			return stats, fmt.Errorf("insert merged finding: %w", err)
		}
		// Re-attach PoC flows from peer finding_flows, remapped.
		ffrows, err := peer.Query(`SELECT flow_id, ord, note FROM finding_flows WHERE finding_id=? ORDER BY ord`, pf.peerID)
		if err == nil {
			for ffrows.Next() {
				var peerFlowID int64
				var ord int
				var note string
				if err := ffrows.Scan(&peerFlowID, &ord, &note); err == nil {
					if localID, ok := peerToLocal[peerFlowID]; ok {
						_ = s.AttachFlow(newID, localID, note, -1)
					}
				}
			}
			ffrows.Close()
		}
		seenFindings[sig] = true
		stats.FindingsAdded++
	}

	return stats, nil
}

// copyBodies copies content-addressed body blobs from a peer bodies dir into this
// store's bodies dir, skipping any already present. Each blob's filename must be a
// valid content hash (path-traversal guard); the content is trusted as-is since it
// is content-addressed and re-verified on read via OpenBody's hash check.
func (s *Store) copyBodies(peerBodiesDir string) (int, error) {
	added := 0
	err := filepath.WalkDir(peerBodiesDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !isContentHash(name) {
			return nil // skip temp/partial or malformed entries
		}
		dst := s.bodyPath(name)
		if _, err := os.Stat(dst); err == nil {
			return nil // already present (dedup)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(p, dst); err != nil {
			return err
		}
		added++
		return nil
	})
	return added, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
	rows, err := db.Query(`SELECT title, target, severity, source, detail FROM findings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.Title, &f.Target, &f.Severity, &f.Source, &f.Detail); err != nil {
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
