package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// maxIntruderRuns is how many finished runs are kept; older ones are pruned.
	maxIntruderRuns = 20
	// maxIntruderRunBytes caps one opaque JSON run record.
	maxIntruderRunBytes = 4 << 20
	maxIntruderRunIDLen = 80
)

// ErrIntruderRunTooLarge is returned when a run record exceeds the cap.
var ErrIntruderRunTooLarge = errors.New("intruder run record too large")

// RunMeta is the listing row for a persisted Intruder run.
type RunMeta struct {
	RunID      string `json:"runId"`
	StartedTS  int64  `json:"startedTs"`
	FinishedTS int64  `json:"finishedTs"`
	Attack     string `json:"attack"`
	Size       int    `json:"size"`
}

// ensureIntruderRuns creates the table on first use. It is additive and
// idempotent, so old databases need no migration step and no existing table
// changes. The record is opaque JSON: store never imports intruder.
func (s *Store) ensureIntruderRuns() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS intruder_runs(
 run_id TEXT PRIMARY KEY, started_ts INTEGER NOT NULL DEFAULT 0, finished_ts INTEGER NOT NULL DEFAULT 0,
 attack TEXT NOT NULL DEFAULT '', record BLOB NOT NULL)`)
	return err
}

func validIntruderRunID(id string) bool {
	if id == "" || len(id) > maxIntruderRunIDLen {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// PutIntruderRun stores (or replaces) a finished run record and prunes to the
// newest maxIntruderRuns by start time. The record is JSON; started/finished
// timestamps and attack are read from it when present to populate listing
// columns, never trusted for anything else.
func (s *Store) PutIntruderRun(id string, rec []byte) error {
	if !validIntruderRunID(id) {
		return fmt.Errorf("invalid intruder run id")
	}
	if len(rec) > maxIntruderRunBytes {
		return ErrIntruderRunTooLarge
	}
	if len(rec) == 0 {
		return fmt.Errorf("empty intruder run record")
	}
	if err := s.ensureIntruderRuns(); err != nil {
		return err
	}
	meta := runMetaFromRecord(rec)
	if meta.StartedTS == 0 {
		meta.StartedTS = time.Now().UnixMilli()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO intruder_runs(run_id,started_ts,finished_ts,attack,record) VALUES(?,?,?,?,?)
 ON CONFLICT(run_id) DO UPDATE SET started_ts=excluded.started_ts, finished_ts=excluded.finished_ts, attack=excluded.attack, record=excluded.record`,
		id, meta.StartedTS, meta.FinishedTS, meta.Attack, rec); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM intruder_runs WHERE run_id NOT IN
 (SELECT run_id FROM intruder_runs ORDER BY started_ts DESC, rowid DESC LIMIT ?)`, maxIntruderRuns); err != nil {
		return err
	}
	return tx.Commit()
}

// GetIntruderRun returns the stored JSON record for id.
func (s *Store) GetIntruderRun(id string) ([]byte, bool, error) {
	if err := s.ensureIntruderRuns(); err != nil {
		return nil, false, err
	}
	var rec []byte
	err := s.db.QueryRow(`SELECT record FROM intruder_runs WHERE run_id=?`, id).Scan(&rec)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

// ListIntruderRuns returns run metadata newest first (limit<=0 means all kept).
func (s *Store) ListIntruderRuns(limit int) []RunMeta {
	out := []RunMeta{}
	if s.ensureIntruderRuns() != nil {
		return out
	}
	if limit <= 0 || limit > maxIntruderRuns {
		limit = maxIntruderRuns
	}
	rows, err := s.db.Query(`SELECT run_id,started_ts,finished_ts,attack,length(record) FROM intruder_runs ORDER BY started_ts DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m RunMeta
		if rows.Scan(&m.RunID, &m.StartedTS, &m.FinishedTS, &m.Attack, &m.Size) == nil {
			out = append(out, m)
		}
	}
	return out
}

// runMetaFromRecord extracts listing fields from the opaque record. The
// record is expected to carry startedTs, finishedTs and attack; any missing
// or malformed field stays zero.
func runMetaFromRecord(rec []byte) RunMeta {
	var v struct {
		StartedTS  int64  `json:"startedTs"`
		FinishedTS int64  `json:"finishedTs"`
		Attack     string `json:"attack"`
	}
	if err := jsonUnmarshalLoose(rec, &v); err != nil {
		return RunMeta{}
	}
	return RunMeta{StartedTS: v.StartedTS, FinishedTS: v.FinishedTS, Attack: strings.TrimSpace(v.Attack)}
}

func jsonUnmarshalLoose(b []byte, v any) error { return json.Unmarshal(b, v) }
