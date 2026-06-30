package store

import (
	"fmt"
	"strconv"
	"strings"
)

// ensureFlowsFTS creates the FTS5 index and backfills it on first run.
func (s *Store) ensureFlowsFTS() error {
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS flows_fts USING fts5(
		host, path, method, note,
		tokenize='unicode61'
	)`); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows_fts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO flows_fts(rowid, host, path, method, note)
		SELECT id, host, path, method, note FROM flows`)
	return err
}

func ftsMatchQuery(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}
	parts := strings.Fields(term)
	bits := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ReplaceAll(p, `"`, `""`)
		bits = append(bits, fmt.Sprintf(`"%s"*`, p))
	}
	return strings.Join(bits, " OR ")
}

// flowSearchAsID returns a flow id when term is an id lookup (#123, id:123, or plain
// digits with scope "id"). Otherwise ok is false and callers should use FTS.
func flowSearchAsID(term, scope string) (int64, bool) {
	term = strings.TrimSpace(term)
	if term == "" {
		return 0, false
	}
	lower := strings.ToLower(term)
	var raw string
	switch {
	case strings.HasPrefix(term, "#"):
		raw = term[1:]
		if raw == "" || strings.TrimSpace(raw) != raw {
			return 0, false
		}
	case strings.HasPrefix(lower, "id:"):
		raw = strings.TrimSpace(term[len("id:"):])
	case scope == "id":
		raw = term
	default:
		return 0, false
	}
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// appendFTSSearch adds an FTS MATCH clause when term is non-empty.
func appendFTSSearch(where []string, args []any, term string) ([]string, []any) {
	term = strings.TrimSpace(term)
	if term == "" {
		return where, args
	}
	q := ftsMatchQuery(term)
	if q == "" {
		return where, args
	}
	where = append(where, "id IN (SELECT rowid FROM flows_fts WHERE flows_fts MATCH ?)")
	args = append(args, q)
	return where, args
}

func (s *Store) indexFlowFTS(id int64, host, path, method, note string) error {
	_, err := s.db.Exec(
		`INSERT INTO flows_fts(rowid, host, path, method, note) VALUES (?,?,?,?,?)`,
		id, host, path, method, note)
	return err
}

func (s *Store) unindexFlowFTS(id int64, host, path, method, note string) error {
	_, err := s.db.Exec(`DELETE FROM flows_fts WHERE rowid=?`, id)
	return err
}

func (s *Store) replaceFlowFTS(id int64, oldHost, oldPath, oldMethod, oldNote, newHost, newPath, newMethod, newNote string) error {
	if err := s.unindexFlowFTS(id, oldHost, oldPath, oldMethod, oldNote); err != nil {
		return err
	}
	return s.indexFlowFTS(id, newHost, newPath, newMethod, newNote)
}
