package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MergePreview reports what MergeFrom would add/skip without mutating this store.
func (s *Store) MergePreview(peerDBPath, peerBodiesDir, label string) (MergeStats, error) {
	var stats MergeStats

	peer, err := sql.Open("sqlite", "file:"+peerDBPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return stats, fmt.Errorf("open peer db: %w", err)
	}
	defer peer.Close()

	if peerBodiesDir != "" {
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
			rel, relErr := filepath.Rel(peerBodiesDir, p)
			if relErr != nil || filepath.Clean(rel) != filepath.Join(name[:2], name[2:4], name) {
				return fmt.Errorf("invalid body archive layout %q", rel)
			}
			actual, digestErr := bodyFileDigest(p)
			if digestErr != nil {
				return digestErr
			}
			if actual != name {
				return fmt.Errorf("body hash mismatch for %s: got %s", name, actual)
			}
			local := s.bodyPath(name)
			if _, err := os.Stat(local); err != nil {
				stats.BodiesAdded++
			}
			return nil
		})
		if err != nil {
			return stats, err
		}
	}

	seenFlows, err := s.flowSignatures(s.db)
	if err != nil {
		return stats, fmt.Errorf("index local flows: %w", err)
	}
	rows, err := peer.Query(`SELECT id, ts, method, scheme, host, port, path, http_version, status,
		req_headers, res_headers, req_body_hash, res_body_hash, req_len, res_len, mime,
		duration_ms, client_addr, error, flags, note FROM flows`)
	if err != nil {
		return stats, fmt.Errorf("read peer flows: %w", err)
	}
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
		if _, ok := seenFlows[flowSig(f)]; ok {
			stats.FlowsSkipped++
		} else {
			stats.FlowsAdded++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}

	seenFindings, err := s.findingSignatures(s.db)
	if err != nil {
		return stats, fmt.Errorf("index local findings: %w", err)
	}
	frows, err := peer.Query(`SELECT id, severity, status, source, title, target, detail,
		evidence, fix, body, impact, why, cwe, environment, cvss, verification_instructions FROM findings`)
	if err != nil {
		return stats, fmt.Errorf("read peer findings: %w", err)
	}
	for frows.Next() {
		var f Finding
		if err := frows.Scan(&f.ID, &f.Severity, &f.Status, &f.Source, &f.Title, &f.Target,
			&f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Environment, &f.Cvss, &f.VerificationInstructions); err != nil {
			frows.Close()
			return stats, err
		}
		if seenFindings[findingSig(f)] {
			stats.FindingsSkipped++
		} else {
			stats.FindingsAdded++
		}
	}
	frows.Close()
	return stats, frows.Err()
}
