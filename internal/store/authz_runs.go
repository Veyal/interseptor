package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Persisted authorization-test run history. The store keeps three opaque JSON
// blobs per run (summary, normalized matrix, raw handler response) so it never
// has to import the control package's run types; only the listing columns
// (ts/kind/actor) are structured.

const (
	// maxAuthzRuns bounds retained history; older runs are pruned on insert.
	maxAuthzRuns = 200
	// maxAuthzRunBytes caps one run's JSON blobs (summary+matrix+raw).
	maxAuthzRunBytes = 4 << 20
	// authzRunKindMax bounds the kind/actor labels written to the columns.
	authzRunLabelMax = 64
	// defaultCredentialScan bounds the credential-usage scan window.
	defaultCredentialScan = 5000
)

// ErrAuthzRunTooLarge is returned when a run record exceeds maxAuthzRunBytes.
var ErrAuthzRunTooLarge = errors.New("authz run record too large")

// AuthzRun is one persisted authorization-test run. Summary is always loaded;
// Matrix and Raw are loaded only by GetAuthzRun.
type AuthzRun struct {
	ID      int64           `json:"id"`
	TS      int64           `json:"ts"` // unix milliseconds
	Kind    string          `json:"kind"`
	Actor   string          `json:"actor"`
	Summary json.RawMessage `json:"summary,omitempty"`
	Matrix  json.RawMessage `json:"matrix,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// CredentialUse is one captured flow whose request carried a credential value.
type CredentialUse struct {
	FlowID int64  `json:"flowId"`
	TS     int64  `json:"ts"` // unix milliseconds
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	Status int    `json:"status"`
}

func authzRunLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > authzRunLabelMax {
		s = s[:authzRunLabelMax]
	}
	return s
}

func authzRunBlob(b json.RawMessage) string {
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// PutAuthzRun appends a run and prunes history to the newest maxAuthzRuns.
// It returns the new run's id.
func (s *Store) PutAuthzRun(run AuthzRun) (int64, error) {
	if len(run.Summary)+len(run.Matrix)+len(run.Raw) > maxAuthzRunBytes {
		return 0, ErrAuthzRunTooLarge
	}
	if authzRunLabel(run.Kind) == "" {
		return 0, fmt.Errorf("authz run kind required")
	}
	ts := run.TS
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO authz_runs(ts,kind,actor,summary,matrix,raw) VALUES(?,?,?,?,?,?)`,
		ts, authzRunLabel(run.Kind), authzRunLabel(run.Actor),
		authzRunBlob(run.Summary), authzRunBlob(run.Matrix), authzRunBlob(run.Raw))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM authz_runs WHERE id NOT IN
 (SELECT id FROM authz_runs ORDER BY id DESC LIMIT ?)`, maxAuthzRuns); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// ListAuthzRuns returns run listings newest first without the matrix/raw
// blobs. limit <= 0 (or above the retention cap) means the whole history.
func (s *Store) ListAuthzRuns(limit int) ([]AuthzRun, error) {
	if limit <= 0 || limit > maxAuthzRuns {
		limit = maxAuthzRuns
	}
	rows, err := s.db.Query(`SELECT id,ts,kind,actor,summary FROM authz_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthzRun{}
	for rows.Next() {
		var r AuthzRun
		var summary string
		if err := rows.Scan(&r.ID, &r.TS, &r.Kind, &r.Actor, &summary); err != nil {
			return nil, err
		}
		r.Summary = json.RawMessage(summary)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAuthzRun returns one run with every blob loaded.
func (s *Store) GetAuthzRun(id int64) (*AuthzRun, bool, error) {
	var r AuthzRun
	var summary, matrix, raw string
	err := s.db.QueryRow(`SELECT id,ts,kind,actor,summary,matrix,raw FROM authz_runs WHERE id=?`, id).
		Scan(&r.ID, &r.TS, &r.Kind, &r.Actor, &summary, &matrix, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	r.Summary, r.Matrix, r.Raw = json.RawMessage(summary), json.RawMessage(matrix), json.RawMessage(raw)
	return &r, true, nil
}

// DeleteAuthzRun removes one run; the bool reports whether a row was deleted.
func (s *Store) DeleteAuthzRun(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM authz_runs WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FlowsWithCredential counts the captured flows whose stored request headers
// contain any of values, and returns the newest `recent` of them.
//
// Request headers are stored as one JSON column, so there is no index to use:
// the scan is deliberately bounded to the newest `scan` flows (defaults to
// 5000) and matched with instr() on the raw column. Credential values are
// matched as substrings of the JSON text, which is exact for cookie and
// Authorization values (neither contains JSON-escaped characters in practice).
func (s *Store) FlowsWithCredential(values []string, scan, recent int) (int, []CredentialUse, error) {
	var clauses []string
	args := []any{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < 8 { // too short to identify a credential; would match anything
			continue
		}
		clauses = append(clauses, "instr(t.req_headers, ?) > 0")
		args = append(args, v)
	}
	if len(clauses) == 0 {
		return 0, nil, nil
	}
	if scan <= 0 {
		scan = defaultCredentialScan
	}
	if recent < 0 {
		recent = 0
	}
	match := "(" + strings.Join(clauses, " OR ") + ")"

	countArgs := append([]any{scan}, args...)
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM
 (SELECT req_headers FROM flows ORDER BY id DESC LIMIT ?) t WHERE `+match, countArgs...).Scan(&count); err != nil {
		return 0, nil, err
	}
	if count == 0 || recent == 0 {
		return count, nil, nil
	}
	rowArgs := append(append([]any{scan}, args...), recent)
	rows, err := s.db.Query(`SELECT t.id,t.ts,t.method,t.host,t.path,COALESCE(t.status,0) FROM
 (SELECT id,ts,method,host,path,status,req_headers FROM flows ORDER BY id DESC LIMIT ?) t
 WHERE `+match+` ORDER BY t.id DESC LIMIT ?`, rowArgs...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := []CredentialUse{}
	for rows.Next() {
		var u CredentialUse
		if err := rows.Scan(&u.FlowID, &u.TS, &u.Method, &u.Host, &u.Path, &u.Status); err != nil {
			return 0, nil, err
		}
		out = append(out, u)
	}
	return count, out, rows.Err()
}
