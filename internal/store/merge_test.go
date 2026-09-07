package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedFlow inserts a flow (with a body) and returns its id.
func seedFlow(t *testing.T, s *Store, host, path, body string, tsMs int64) int64 {
	t.Helper()
	var hash string
	if body != "" {
		bw, err := s.NewBodyWriter()
		if err != nil {
			t.Fatalf("NewBodyWriter: %v", err)
		}
		bw.Write([]byte(body))
		hash, _, err = bw.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
	}
	id, err := s.InsertFlow(&Flow{
		TS: time.UnixMilli(tsMs), Method: "GET", Scheme: "https", Host: host, Port: 443,
		Path: path, Status: 200, ResBodyHash: hash, ResLen: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return id
}

func peerBodiesDir(s *Store) string { return s.BodiesDir() }

func TestQueryPeerFindingsSupportsLegacySchemaWithoutEnvelopeColumns(t *testing.T) {
	peerPath := filepath.Join(t.TempDir(), "legacy.db")
	peer, err := sql.Open("sqlite", peerPath)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	defer peer.Close()
	if _, err := peer.Exec(`CREATE TABLE findings (
		id INTEGER PRIMARY KEY, severity TEXT, status TEXT, source TEXT, title TEXT,
		target TEXT, detail TEXT, evidence TEXT, fix TEXT
	)`); err != nil {
		t.Fatalf("create legacy findings: %v", err)
	}
	if _, err := peer.Exec(`INSERT INTO findings VALUES (
		1, 'High', 'open', 'human', 'Legacy issue', 'https://example.com',
		'detail', 'evidence', 'fix'
	)`); err != nil {
		t.Fatalf("insert legacy finding: %v", err)
	}

	rows, err := queryPeerFindings(peer)
	if err != nil {
		t.Fatalf("queryPeerFindings: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected legacy finding row")
	}
	var f Finding
	if err := rows.Scan(&f.ID, &f.Severity, &f.Status, &f.Source, &f.Title, &f.Target,
		&f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe,
		&f.Environment, &f.Cvss, &f.VerificationInstructions, &f.Summary,
		&f.Confidence, &f.Retest, &f.Targets, &f.ProofReview); err != nil {
		t.Fatalf("scan legacy finding: %v", err)
	}
	if f.Title != "Legacy issue" || f.Body != "" || f.Impact != "" || f.Why != "" || f.Cwe != "" || f.Environment != "" || f.Cvss != "" || f.VerificationInstructions != "" || f.Summary != "" || f.Confidence != "" || f.Retest != "" {
		t.Fatalf("legacy finding fallback = %+v", f)
	}
}

func TestMergeFromLegacyFindingWithoutBodyPreservesFindingFlows(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	flowID := seedFlow(t, peer, "example.com", "/account/2", "private order", 1000)
	findingID, err := peer.CreateFinding(&Finding{
		Severity: "High", Title: "Legacy access control", Target: "https://example.com/account/2",
		Detail: "Cross-account access is possible.", Impact: "Private data is disclosed.", Why: "Ownership is not checked.",
	})
	if err != nil {
		t.Fatalf("create peer finding: %v", err)
	}
	if err := peer.AttachFlow(findingID, flowID, "After: private data returned", -1); err != nil {
		t.Fatalf("attach peer flow: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	// Simulate the original findings schema, before the body, risk/classification,
	// tags, and evidence-first envelope columns were introduced. The
	// finding_flows table intentionally remains so its legacy PoC attachment
	// must still be imported.
	for _, column := range []string{"body", "summary", "confidence", "retest", "impact", "why", "cwe", "environment", "cvss", "verification_instructions"} {
		if _, err := peer.db.Exec(`ALTER TABLE findings DROP COLUMN ` + column); err != nil {
			peer.Close()
			t.Fatalf("drop legacy %s column: %v", column, err)
		}
	}
	if _, err := peer.db.Exec(`DROP TABLE finding_tags`); err != nil {
		peer.Close()
		t.Fatalf("drop legacy finding_tags table: %v", err)
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "legacy")
	if err != nil {
		t.Fatalf("merge legacy finding: %v", err)
	}
	if stats.FindingsAdded != 1 || stats.FlowsAdded != 1 {
		t.Fatalf("merge stats=%+v", stats)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil || len(findings) != 1 {
		t.Fatalf("merged findings=%d err=%v", len(findings), err)
	}
	if len(findings[0].Flows) != 1 || findings[0].Flows[0].Path != "/account/2" {
		t.Fatalf("legacy finding flows=%+v", findings[0].Flows)
	}
	if len(findings[0].Blocks) != 2 || findings[0].Blocks[1].FlowID == 0 {
		t.Fatalf("legacy finding blocks=%+v", findings[0].Blocks)
	}
}

func TestMergeFromPreservesMissingCanonicalAndLegacyFlowEvidence(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{
		Severity: "High", Title: "Purged evidence", Target: "https://example.com/account",
		Detail: "The captured proof was retained after flow retention removed the exchanges.",
	})
	if err != nil {
		t.Fatalf("create peer finding: %v", err)
	}
	// Simulate a peer whose finding body and legacy attachment table outlived
	// their flow rows after history pruning. CreateFinding normally rejects new
	// orphan references, so this models the persisted post-prune state directly.
	body := marshalBody([]FindingBlock{
		{Type: "text", MD: "Retained report narrative."},
		{Type: "flow", FlowID: 901, Note: "canonical proof", Role: "result", Proof: "response proves disclosure", Source: "captured_flow", SourceFlowID: 901},
	})
	if _, err := peer.db.Exec(`UPDATE findings SET body=? WHERE id=?`, body, findingID); err != nil {
		t.Fatalf("seed canonical orphan body: %v", err)
	}
	if _, err := peer.db.Exec(`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)`, findingID, 902, 2, "legacy proof"); err != nil {
		t.Fatalf("seed legacy orphan attachment: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	for i := int64(0); i < 900; i++ {
		seedFlow(t, local, "example.com", "/padding", "x", i+1)
	}
	if id := seedFlow(t, local, "example.com", "/unrelated", "local", 901); id != 901 {
		t.Fatalf("local collision flow id=%d, want 901", id)
	}
	stats, err := local.MergeFrom(peerDBPath, "", "purged")
	if err != nil {
		t.Fatalf("merge orphan evidence: %v", err)
	}
	if stats.FindingsAdded != 1 {
		t.Fatalf("merge stats=%+v, want one finding", stats)
	}
	got, err := local.GetFinding(1)
	if err != nil {
		t.Fatalf("get merged finding: %v", err)
	}
	if len(got.Flows) != 2 || len(got.Blocks) != 3 {
		t.Fatalf("merged evidence must expose both missing references: %+v", got)
	}
	for _, flow := range got.Flows {
		if !flow.Missing || flow.Method != "" || flow.Host != "" {
			t.Fatalf("missing projection leaked local flow: %+v", flow)
		}
	}
	var attached int
	if err := local.db.QueryRow(`SELECT count(*) FROM finding_flows WHERE finding_id=?`, got.ID).Scan(&attached); err != nil || attached != 0 {
		t.Fatalf("missing references attached to live IDs: %d %v", attached, err)
	}
	for _, block := range got.Blocks {
		if block.Type == "flow" && !block.Missing {
			t.Fatalf("orphan body flow should be visible as missing: %+v", block)
		}
		if block.Type == "flow" && block.FlowID == 901 && block.SourceFlowID != 0 {
			t.Fatalf("orphan provenance retained colliding peer flow id: %+v", block)
		}
	}
}

func TestMergeFromRejectsInvalidConfidenceBeforeImport(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	seedFlow(t, peer, "example.com", "/peer", "peer", 1)
	findingID, err := peer.CreateFinding(&Finding{Title: "invalid confidence"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.db.Exec(`UPDATE findings SET confidence='guess' WHERE id=?`, findingID); err != nil {
		t.Fatal(err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, "", "invalid"); err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("MergeFrom error=%v, want confidence preflight rejection", err)
	}
	flows, err := local.QueryFlows(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 0 {
		t.Fatalf("preflight failure imported flows: %+v", flows)
	}
}

func TestMergeRejectsOversizedBodyMadeOnlyOfMissingFlowEvidence(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{Title: "Oversized missing evidence"})
	if err != nil {
		peer.Close()
		t.Fatalf("create peer finding: %v", err)
	}
	var body strings.Builder
	body.WriteByte('[')
	for id := int64(1); body.Len() <= maxFindingBodyBytes+1024; id++ {
		if id > 1 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"type":"flow","flowId":%d,"note":"%s"}`, id, strings.Repeat("x", 128))
	}
	body.WriteByte(']')
	if _, err := peer.db.Exec(`UPDATE findings SET body=? WHERE id=?`, body.String(), findingID); err != nil {
		peer.Close()
		t.Fatalf("seed oversized peer body: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "oversized"); err == nil || !strings.Contains(err.Error(), "body too large") {
		t.Fatalf("MergeFrom error=%v, want body-size rejection", err)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("oversized merge persisted %d findings", len(findings))
	}
}

func TestMergePreflightsTableOnlyEvidenceBeforeImportingFlows(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	flowID := seedFlow(t, peer, "example.com", "/proof", "bounded", 1)
	findingID, err := peer.CreateFinding(&Finding{Title: "Oversized table evidence", Detail: "bounded narrative"})
	if err != nil {
		peer.Close()
		t.Fatalf("create peer finding: %v", err)
	}
	if _, err := peer.db.Exec(
		`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)`,
		findingID, flowID, 0, strings.Repeat("n", maxFindingBodyBytes),
	); err != nil {
		peer.Close()
		t.Fatalf("seed table-only evidence: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "oversized-table"); err == nil || !strings.Contains(err.Error(), "body too large") {
		t.Fatalf("MergeFrom error=%v, want preflight body-size rejection", err)
	}
	var flows, findings int
	if err := local.db.QueryRow(`SELECT COUNT(*) FROM flows`).Scan(&flows); err != nil {
		t.Fatal(err)
	}
	if err := local.db.QueryRow(`SELECT COUNT(*) FROM findings`).Scan(&findings); err != nil {
		t.Fatal(err)
	}
	if flows != 0 || findings != 0 {
		t.Fatalf("failed preflight mutated local database: flows=%d findings=%d", flows, findings)
	}
}

func TestFindingMissingMarkerWinsOverLocalFlowIDCollision(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	for i := int64(0); i < 900; i++ {
		seedFlow(t, s, "example.com", "/padding", "x", i+1)
	}
	localID := seedFlow(t, s, "example.com", "/unrelated", "local", 901)
	if localID != 901 {
		t.Fatalf("local flow id=%d, want 901", localID)
	}
	body := `[ {"type":"flow","flowId":901,"note":"purged peer proof","missing":true} ]`
	fid, err := s.CreateFinding(&Finding{Title: "Collision", Target: "https://example.com", Body: body})
	if err != nil {
		t.Fatalf("create finding: %v", err)
	}
	got, err := s.GetFinding(fid)
	if err != nil || len(got.Blocks) != 1 || !got.Blocks[0].Missing {
		t.Fatalf("missing marker was resolved: finding=%+v err=%v", got, err)
	}
	if len(got.Flows) != 1 || !got.Flows[0].Missing || got.Flows[0].Method != "" || got.Flows[0].Host != "" {
		t.Fatalf("missing projection resolved unrelated traffic: %+v", got.Flows)
	}
	var attached int
	if err := s.db.QueryRow(`SELECT count(*) FROM finding_flows WHERE finding_id=?`, fid).Scan(&attached); err != nil || attached != 0 {
		t.Fatalf("missing ref persisted live attachment: %d %v", attached, err)
	}
	if err := s.UpdateFinding(fid, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("resave marked missing body: %v", err)
	}
}

func TestMergeFromRejectsPeerFlowIterationErrorBeforeImport(t *testing.T) {
	peerPath := filepath.Join(t.TempDir(), "peer.db")
	peer, err := sql.Open("sqlite", peerPath)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	statements := []string{
		`CREATE TABLE source (id INTEGER PRIMARY KEY)`,
		`INSERT INTO source(id) VALUES (1), (2)`,
		`CREATE VIEW flows AS SELECT id, id AS ts, 'GET' AS method, 'https' AS scheme,
			'example.com' AS host, 443 AS port, '/item/' || id AS path, 'HTTP/1.1' AS http_version,
			CASE WHEN id=2 THEN json_extract('invalid json', '$') ELSE 200 END AS status,
			'{}' AS req_headers, '{}' AS res_headers, '' AS req_body_hash, '' AS res_body_hash,
			0 AS req_len, 0 AS res_len, 'text/plain' AS mime, 0 AS duration_ms,
			'' AS client_addr, '' AS error, 0 AS flags, '' AS note FROM source ORDER BY id`,
		`CREATE TABLE findings (id INTEGER, severity TEXT, status TEXT, source TEXT, title TEXT,
			target TEXT, detail TEXT, evidence TEXT, fix TEXT, body TEXT, impact TEXT, why TEXT,
			cwe TEXT, environment TEXT, cvss TEXT, verification_instructions TEXT)`,
	}
	for _, statement := range statements {
		if _, err := peer.Exec(statement); err != nil {
			peer.Close()
			t.Fatalf("prepare peer: %v", err)
		}
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerPath, "", "peer"); err == nil || !strings.Contains(err.Error(), "iterate peer flows") {
		t.Fatalf("MergeFrom error = %v, want peer flow iteration error", err)
	}
	if flows, err := local.QueryFlows(10); err != nil || len(flows) != 0 {
		t.Fatalf("iteration failure partially imported flows=%d err=%v", len(flows), err)
	}
}

func TestMergeFromRejectsPeerFindingIterationErrorBeforeImport(t *testing.T) {
	peerPath := filepath.Join(t.TempDir(), "peer.db")
	peer, err := sql.Open("sqlite", peerPath)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	statements := []string{
		`CREATE TABLE source (id INTEGER PRIMARY KEY)`,
		`INSERT INTO source(id) VALUES (1), (2)`,
		`CREATE TABLE flows (id INTEGER, ts INTEGER, method TEXT, scheme TEXT, host TEXT, port INTEGER,
			path TEXT, http_version TEXT, status INTEGER, req_headers TEXT, res_headers TEXT,
			req_body_hash TEXT, res_body_hash TEXT, req_len INTEGER, res_len INTEGER, mime TEXT,
			duration_ms INTEGER, client_addr TEXT, error TEXT, flags INTEGER, note TEXT)`,
		`CREATE VIEW findings AS SELECT id,
			CASE WHEN id=2 THEN json_extract('invalid json', '$') ELSE 'High' END AS severity,
			'open' AS status, 'human' AS source, 'finding-' || id AS title, 'https://example.com' AS target,
			'' AS detail, '' AS evidence, '' AS fix, '' AS body, '' AS impact, '' AS why,
			'' AS cwe, '' AS environment, '' AS cvss, '' AS verification_instructions
			FROM source ORDER BY id`,
		`CREATE TABLE finding_flows (finding_id INTEGER, flow_id INTEGER, ord INTEGER, note TEXT)`,
		`CREATE TABLE finding_tags (finding_id INTEGER, tag TEXT)`,
	}
	for _, statement := range statements {
		if _, err := peer.Exec(statement); err != nil {
			peer.Close()
			t.Fatalf("prepare peer: %v", err)
		}
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("close peer: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerPath, "", "peer"); err == nil || !strings.Contains(err.Error(), "iterate peer findings") {
		t.Fatalf("MergeFrom error = %v, want peer finding iteration error", err)
	}
	if findings, err := local.ListFindings("", "", ""); err != nil || len(findings) != 0 {
		t.Fatalf("iteration failure partially imported findings=%d err=%v", len(findings), err)
	}
}

func TestMergeFromRejectsBodyWhoseContentDoesNotMatchFilename(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	var bodyPath string
	err = filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && len(d.Name()) == 64 {
			bodyPath = path
		}
		return err
	})
	if err != nil || bodyPath == "" {
		t.Fatalf("find peer body: path=%q err=%v", bodyPath, err)
	}
	if err := os.WriteFile(bodyPath, []byte("corrupt body"), 0o644); err != nil {
		t.Fatalf("corrupt body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("MergeFrom error = %v, want hash mismatch", err)
	}
	if flows, _ := local.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("corrupt merge mutated flows: %d", len(flows))
	}
}

func TestMergeFromRollsBackFlowWhenProvenanceTagFails(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	peerID := seedFlow(t, peer, "example.com", "/", "", 1000)
	if err := peer.SetFlowNote(peerID, "peer note"); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, err := local.db.Exec(`CREATE TRIGGER reject_merge_tag BEFORE INSERT ON flow_tags
		BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := local.MergeFrom(peerDBPath, peerBodies, "alice"); err == nil {
		t.Fatal("MergeFrom reported success after provenance tag failure")
	}
	flows, err := local.QueryFlows(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 0 {
		t.Fatalf("failed merge left %d flow(s), want transactional rollback", len(flows))
	}
	if _, err := local.db.Exec(`DROP TRIGGER reject_merge_tag`); err != nil {
		t.Fatal(err)
	}
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "alice"); err != nil {
		t.Fatalf("retry after removing failure: %v", err)
	}
	flows, err = local.QueryFlows(10)
	if err != nil || len(flows) != 1 {
		t.Fatalf("retry flows = %d, err = %v", len(flows), err)
	}
	if err := local.AttachTags(flows); err != nil {
		t.Fatal(err)
	}
	if flows[0].Note != "peer note" || len(flows[0].Tags) != 1 || flows[0].Tags[0] != "peer-alice" {
		t.Fatalf("merged metadata = note %q, tags %v", flows[0].Note, flows[0].Tags)
	}
}

func TestMergeFromRollsBackFindingWhenTagInsertFails(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.CreateFinding(&Finding{Title: "peer finding", Tags: []string{"api"}}); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, err := local.db.Exec(`CREATE TRIGGER reject_merged_finding_tag BEFORE INSERT ON finding_tags
		BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := local.MergeFrom(peerDBPath, peerBodies, "alice"); err == nil {
		t.Fatal("MergeFrom reported success after finding-tag failure")
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("failed merge left %d finding(s), want transactional rollback", len(findings))
	}
}

func TestMergeFromDoesNotPublishBodiesBeforeAllCopiesValidate(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	corruptHash := strings.Repeat("f", 64)
	corruptPath := filepath.Join(peerBodies, "ff", "ff", corruptHash)
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o755); err != nil {
		t.Fatalf("mkdir corrupt body: %v", err)
	}
	if err := os.WriteFile(corruptPath, []byte("wrong"), 0o644); err != nil {
		t.Fatalf("write corrupt body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil {
		t.Fatal("MergeFrom accepted corrupt body")
	}
	var published []string
	_ = filepath.WalkDir(local.BodiesDir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			published = append(published, d.Name())
		}
		return err
	})
	if len(published) != 0 {
		t.Fatalf("merge published bodies before validation completed: %v", published)
	}
}

func TestMergeFromIgnoresActiveTempBodyFiles(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "valid body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	if err := os.WriteFile(filepath.Join(peerBodies, ".tmp-active"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "peer")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != 1 || stats.BodiesAdded != 1 {
		t.Fatalf("stats=%+v, want one flow and body", stats)
	}
}

func TestMergeFromRejectsFlowReferencingMissingBody(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "required body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	if err := filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			return os.Remove(path)
		}
		return err
	}); err != nil {
		t.Fatalf("remove body: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergePreview(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing body") {
		t.Fatalf("MergePreview error=%v, want missing body", err)
	}
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing body") {
		t.Fatalf("MergeFrom error=%v, want missing body", err)
	}
	if flows, _ := local.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("missing-body merge inserted %d flows", len(flows))
	}
}

func TestMergeFromFailsWhenRequiredBodyCannotBeRead(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "required body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	var bodyPath string
	_ = filepath.WalkDir(peerBodies, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isContentHash(d.Name()) {
			bodyPath = path
		}
		return err
	})
	if bodyPath == "" {
		t.Fatal("body path not found")
	}
	if err := os.Remove(bodyPath); err != nil {
		t.Fatalf("remove body: %v", err)
	}
	if err := os.Symlink(filepath.Join(peerDir, "missing-target"), bodyPath); err != nil {
		t.Fatalf("create broken body symlink: %v", err)
	}
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil {
		t.Fatal("MergeFrom ignored unreadable required body")
	}
}

func TestMergeFromProtectsPublishedBodiesFromConcurrentGC(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "merge race body", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()

	bodiesPublished := make(chan struct{})
	continueMerge := make(chan struct{})
	mergeDone := make(chan error, 1)
	go func() {
		_, err := local.mergeFrom(peerDBPath, peerBodies, "peer", mergeHooks{
			afterBodiesPublished: func() {
				close(bodiesPublished)
				<-continueMerge
			},
		})
		mergeDone <- err
	}()
	<-bodiesPublished
	removed, _, err := local.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed %d merge body before references committed", removed)
	}
	close(continueMerge)
	if err := <-mergeDone; err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
}

func TestConcurrentBodyPublishDeduplicatesRenameLoser(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "shared merge body", 1000)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()

	arrived := make(chan struct{}, 2)
	releaseRename := make(chan struct{})
	var renameMu sync.Mutex
	ops := bodyPublishOps{
		beforeRename: func(string) {
			arrived <- struct{}{}
			<-releaseRename
		},
		rename: func(oldPath, newPath string) error {
			renameMu.Lock()
			defer renameMu.Unlock()
			if _, err := os.Stat(newPath); err == nil {
				return os.ErrExist // emulate Windows rename-no-replace behavior
			}
			return os.Rename(oldPath, newPath)
		},
	}
	type result struct {
		added int
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			added, release, err := local.copyBodiesWithOps(peerBodies, ops)
			release()
			results <- result{added: added, err: err}
		}()
	}
	<-arrived
	<-arrived // both publishers observed destination absent
	close(releaseRename)

	totalAdded := 0
	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent publish: %v", res.err)
		}
		totalAdded += res.added
	}
	if totalAdded != 1 {
		t.Fatalf("total added=%d, want one published blob", totalAdded)
	}
}

func TestBodyPublishRenameLoserRejectsMismatchedDestination(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/", "expected merge body", 1000)
	peerBodies := peer.BodiesDir()
	peer.Close()
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	ops := bodyPublishOps{
		beforeRename: func(dst string) {
			if err := os.WriteFile(dst, []byte("wrong destination content"), 0o644); err != nil {
				t.Errorf("write conflicting destination: %v", err)
			}
		},
		rename: func(string, string) error { return os.ErrExist },
	}
	_, release, err := local.copyBodiesWithOps(peerBodies, ops)
	release()
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("copyBodies error=%v, want destination hash mismatch", err)
	}
}

func TestMergeFromRejectsFindingReferencingMissingImageBody(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	hash, _, err := peer.PutImageBytes("image/png", tinyPNG)
	if err != nil {
		t.Fatalf("PutImageBytes: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{Title: "Evidence", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := peer.AttachImage(findingID, hash, "image/png", "proof", -1); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}
	if err := os.Remove(peer.bodyPath(hash)); err != nil {
		t.Fatalf("remove image body: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergePreview(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("MergePreview error=%v, want missing image body", err)
	}
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("MergeFrom error=%v, want missing image body", err)
	}
	findings, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("missing-image merge inserted %d finding(s)", len(findings))
	}
}

func TestMergePreviewRejectsMalformedFindingBody(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{Title: "Malformed", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := peer.db.Exec(`UPDATE findings SET body='{bad json' WHERE id=?`, findingID); err != nil {
		t.Fatalf("corrupt finding body: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergePreview(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "body must be") {
		t.Fatalf("MergePreview error=%v, want invalid finding body", err)
	}
}

func TestMergeFromValidatesNormalizedFindingImageBlocks(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	findingID, err := peer.CreateFinding(&Finding{Title: "Evidence", Target: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	missingHash := strings.Repeat("f", 64)
	body := `[{"type":"IMAGE","hash":"` + missingHash + `","mime":"image/png"}]`
	if _, err := peer.db.Exec(`UPDATE findings SET body=? WHERE id=?`, body, findingID); err != nil {
		t.Fatalf("set noncanonical image body: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "missing image body") {
		t.Fatalf("MergeFrom error=%v, want missing normalized image body", err)
	}
	if findings, err := local.ListFindings("", "", ""); err != nil || len(findings) != 0 {
		t.Fatalf("invalid image merge imported findings=%d err=%v", len(findings), err)
	}
}

func TestMergePreviewMatchesFirstMergeCounts(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	f1 := seedFlow(t, peer, "victim.test", "/a", "resp-a", 1000)
	seedFlow(t, peer, "victim.test", "/b", "resp-b", 2000)
	_, _ = peer.CreateFinding(&Finding{Severity: "High", Title: "IDOR", Target: "https://victim.test/a"})
	_ = f1
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peerBodiesDir(peer)
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	seedFlow(t, local, "other.test", "/x", "resp-x", 500)

	prev, err := local.MergePreview(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergePreview: %v", err)
	}
	if prev.FlowsAdded != 2 || prev.FindingsAdded != 1 {
		t.Fatalf("preview = %+v; want 2 flows / 1 finding", prev)
	}
	flows, _ := local.QueryFlows(100)
	if len(flows) != 1 {
		t.Fatalf("preview must not mutate; got %d flows", len(flows))
	}
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != prev.FlowsAdded || stats.FindingsAdded != prev.FindingsAdded {
		t.Fatalf("merge %+v != preview %+v", stats, prev)
	}
}

func TestMergePreviewAccountsForDuplicatesWithinPeer(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/same", "same body", 1000)
	seedFlow(t, peer, "example.com", "/same", "same body", 1000)
	for range 2 {
		if _, err := peer.CreateFinding(&Finding{
			Severity: "High", Source: "human", Title: "Duplicate", Target: "https://example.com/same", Detail: "same detail",
		}); err != nil {
			t.Fatalf("CreateFinding: %v", err)
		}
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peer.BodiesDir()
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	preview, err := local.MergePreview(peerDBPath, peerBodies, "peer")
	if err != nil {
		t.Fatalf("MergePreview: %v", err)
	}
	if preview.FlowsAdded != 1 || preview.FlowsSkipped != 1 || preview.FindingsAdded != 1 || preview.FindingsSkipped != 1 {
		t.Fatalf("preview = %+v, want one add and one skip for each duplicate kind", preview)
	}
	merged, err := local.MergeFrom(peerDBPath, peerBodies, "peer")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if preview.FlowsAdded != merged.FlowsAdded || preview.FlowsSkipped != merged.FlowsSkipped ||
		preview.FindingsAdded != merged.FindingsAdded || preview.FindingsSkipped != merged.FindingsSkipped {
		t.Fatalf("preview %+v does not match merge %+v", preview, merged)
	}
}

func TestMergePreviewDoesNotRecountExistingBodies(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	seedFlow(t, peer, "example.com", "/body", "shared response", 1000)
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peerBodiesDir(peer)
	peer.Close()

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergeFrom(peerDBPath, peerBodies, "peer"); err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}

	preview, err := local.MergePreview(peerDBPath, peerBodies, "peer")
	if err != nil {
		t.Fatalf("MergePreview: %v", err)
	}
	if preview.BodiesAdded != 0 {
		t.Fatalf("BodiesAdded = %d, want 0 for content already present", preview.BodiesAdded)
	}
}

func TestMergePreviewRejectsInvalidBodyArchiveEntry(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peer.Close()
	peerBodies := filepath.Join(peerDir, "bodies")
	invalid := strings.Repeat("z", 64)
	invalidPath := filepath.Join(peerBodies, invalid[:2], invalid[2:4], invalid)
	if err := os.MkdirAll(filepath.Dir(invalidPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(invalidPath, []byte("not a body hash"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	if _, err := local.MergePreview(peerDBPath, peerBodies, "peer"); err == nil || !strings.Contains(err.Error(), "invalid body archive entry") {
		t.Fatalf("MergePreview error = %v, want invalid body archive entry", err)
	}
}

func TestMergeFromUnionsAndIsIdempotent(t *testing.T) {
	// Peer project with 2 flows + 1 finding referencing a flow.
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatalf("open peer: %v", err)
	}
	f1 := seedFlow(t, peer, "victim.test", "/a", "resp-a", 1000)
	seedFlow(t, peer, "victim.test", "/b", "resp-b", 2000)
	body := marshalBody([]FindingBlock{
		{Type: "text", MD: "IDOR on /a"},
		{Type: "flow", FlowID: f1, Note: "poc", Source: "captured_flow", SourceFlowID: f1},
	})
	fid, err := peer.CreateFinding(&Finding{Severity: "High", Status: "verified", Source: "ai",
		Title: "IDOR", Summary: "A user can read another user's record.", Target: "https://victim.test/a", Confidence: "certain",
		Detail: "IDOR on /a", Body: body, Retest: "Cross-user access returns a uniform denial."})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := peer.AttachFlow(fid, f1, "poc", -1); err != nil {
		t.Fatalf("AttachFlow: %v", err)
	}
	peerDBPath := filepath.Join(peerDir, currentDBName)
	peerBodies := peerBodiesDir(peer)
	peer.Close()

	// Local project with 1 pre-existing flow.
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer local.Close()
	seedFlow(t, local, "other.test", "/x", "resp-x", 500)

	// First merge: both peer flows + the finding land; body flow-ref remapped.
	stats, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if stats.FlowsAdded != 2 || stats.FindingsAdded != 1 {
		t.Fatalf("first merge stats = %+v; want 2 flows / 1 finding added", stats)
	}
	flows, _ := local.QueryFlows(100)
	if len(flows) != 3 {
		t.Fatalf("expected 3 flows after merge, got %d", len(flows))
	}
	finds, _ := local.ListFindings("", "", "")
	if len(finds) != 1 {
		t.Fatalf("expected 1 merged finding, got %d", len(finds))
	}
	// The finding's PoC flow-ref must point at a LOCAL flow id that exists.
	f := finds[0]
	if f.Summary != "A user can read another user's record." || f.Confidence != "certain" || f.Retest != "Cross-user access returns a uniform denial." {
		t.Fatalf("merged finding lost evidence-first fields: summary=%q confidence=%q retest=%q", f.Summary, f.Confidence, f.Retest)
	}
	var pocFlowID int64
	for _, b := range f.Blocks {
		if b.Type == "flow" {
			pocFlowID = b.FlowID
			if b.SourceFlowID != b.FlowID {
				t.Fatalf("source flow provenance was not remapped with evidence: %+v", b)
			}
		}
	}
	if pocFlowID == 0 {
		t.Fatal("merged finding lost its PoC flow block")
	}
	if _, err := local.GetFlow(pocFlowID); err != nil {
		t.Fatalf("remapped PoC flow id %d does not resolve locally: %v", pocFlowID, err)
	}

	// Second merge of the SAME peer: idempotent — nothing new added.
	stats2, err := local.MergeFrom(peerDBPath, peerBodies, "alice")
	if err != nil {
		t.Fatalf("second MergeFrom: %v", err)
	}
	if stats2.FlowsAdded != 0 || stats2.FindingsAdded != 0 {
		t.Fatalf("re-merge must be idempotent, got %+v", stats2)
	}
	if stats2.FlowsSkipped != 2 || stats2.FindingsSkipped != 1 {
		t.Fatalf("re-merge should skip all, got %+v", stats2)
	}
	if flows, _ := local.QueryFlows(100); len(flows) != 3 {
		t.Fatalf("re-merge must not duplicate flows, got %d", len(flows))
	}
}

func TestMergeInitialRevisionPreservesMissingEvidenceAndProvenance(t *testing.T) {
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	generated, err := local.CreateFinding(&Finding{Title: "Generated preview"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _, err := local.PutAndAttachImage(generated, "image/png", tinyPNG, "Preview", -1, "result", "Preview only", "flow_preview", 0)
	if err != nil {
		t.Fatal(err)
	}
	seedFlow(t, local, "example.com", "/unrelated", "", 1000)
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &Finding{Title: "Imported evidence", Body: fmt.Sprintf(`[{"type":"flow","flowId":1,"missing":true,"note":"Retained missing evidence"},{"type":"image","hash":%q,"mime":"image/png","source":"browser_screenshot"}]`, hash)}
	if _, err = peer.CreateFinding(f); err != nil {
		t.Fatal(err)
	}
	if _, _, err = peer.PutAndAttachImage(f.ID, "image/png", tinyPNG, "Capture", -1, "result", "Claimed screenshot", "browser_screenshot", 0); err != nil {
		t.Fatal(err)
	}
	bodies := peer.BodiesDir()
	peer.Close()
	if _, err = local.MergeFrom(filepath.Join(peerDir, currentDBName), bodies, "peer"); err != nil {
		t.Fatal(err)
	}
	fs, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var imported Finding
	for _, candidate := range fs {
		if candidate.Title == f.Title {
			imported = candidate
		}
	}
	check := func(got *Finding) {
		t.Helper()
		if len(got.Blocks) != 2 || !got.Blocks[0].Missing || got.Blocks[0].Note != "Retained missing evidence" || got.Blocks[1].Source != "flow_preview" || got.Blocks[1].Provenance == nil || got.Blocks[1].Provenance.Ingestion != "generated" {
			t.Fatalf("incomplete or untruthful imported evidence: %+v", got.Blocks)
		}
		var n int
		if err := local.db.QueryRow(`SELECT COUNT(*) FROM finding_flows WHERE finding_id=?`, got.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unsafe associations: %d %v", n, err)
		}
	}
	check(&imported)
	revisions, err := local.ListFindingRevisions(imported.ID, 100)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("initial revisions: %+v %v", revisions, err)
	}
	if err = local.RestoreFindingRevision(imported.ID, revisions[0].ID, FindingChange{}); err != nil {
		t.Fatal(err)
	}
	restored, err := local.GetFinding(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(restored)
}
