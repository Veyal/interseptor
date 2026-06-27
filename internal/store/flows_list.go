package store

import (
	"strconv"
	"strings"
	"time"
)

// flowListColumns is the slim SELECT for history lists — no header JSON blobs.
const flowListColumns = `id, ts, method, scheme, host, port, path, http_version, status,
	req_len, res_len, mime, duration_ms, client_addr, error, flags, note`

// QueryFlowsListFilter is like QueryFlowsFilter but skips req/res header columns.
func (s *Store) QueryFlowsListFilter(f FlowFilter) ([]*Flow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	where, args := buildFlowFilterWhere(f)
	where, args = appendFlowPageCursor(f, where, args)
	q := "SELECT " + flowListColumns + " FROM flows"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += flowListOrderBy(f) + " LIMIT " + strconv.Itoa(limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Flow
	for rows.Next() {
		fl, err := scanFlowList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fl)
	}
	return out, rows.Err()
}

func scanFlowList(row scanner) (*Flow, error) {
	var (
		f        Flow
		tsMillis int64
	)
	if err := row.Scan(
		&f.ID, &tsMillis, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path, &f.HTTPVersion, &f.Status,
		&f.ReqLen, &f.ResLen, &f.Mime, &f.DurationMs, &f.ClientAddr, &f.Error, &f.Flags, &f.Note,
	); err != nil {
		return nil, err
	}
	f.TS = time.UnixMilli(tsMillis).UTC()
	return &f, nil
}

// buildFlowFilterWhere returns SQL WHERE fragments and args for FlowFilter.
func buildFlowFilterWhere(f FlowFilter) ([]string, []any) {
	var (
		where []string
		args  []any
	)
	if f.Method != "" {
		where = append(where, "method = ?")
		args = append(args, f.Method)
	}
	if f.Scheme != "" {
		where = append(where, "scheme = ?")
		args = append(args, f.Scheme)
	}
	if f.Host != "" {
		where = append(where, "instr(lower(host), lower(?)) > 0")
		args = append(args, f.Host)
	}
	if f.Search != "" {
		where, args = appendFTSSearch(where, args, f.Search)
	}
	if f.StatusClass >= 1 && f.StatusClass <= 5 {
		lo := f.StatusClass * 100
		where = append(where, "status >= ? AND status < ?")
		args = append(args, lo, lo+100)
	}
	if f.RequireFlags != 0 {
		where = append(where, "(flags & ?) != 0")
		args = append(args, f.RequireFlags)
	}
	if f.WithoutFlags != 0 {
		where = append(where, "(flags & ?) = 0")
		args = append(args, f.WithoutFlags)
	}
	if f.ExcludeFlags != 0 {
		if f.IncludeFlags != 0 {
			where = append(where, "((flags & ?) = 0 OR (flags & ?) != 0)")
			args = append(args, f.ExcludeFlags, f.IncludeFlags)
		} else {
			where = append(where, "(flags & ?) = 0")
			args = append(args, f.ExcludeFlags)
		}
	}
	for _, m := range f.NotMethods {
		where = append(where, "method <> ?")
		args = append(args, m)
	}
	for _, h := range f.NotHosts {
		where = append(where, "instr(lower(host), lower(?)) = 0")
		args = append(args, h)
	}
	for _, p := range f.NotPaths {
		where = append(where, "instr(lower(path), lower(?)) = 0")
		args = append(args, p)
	}
	for _, st := range f.NotStatuses {
		where = append(where, "status <> ?")
		args = append(args, st)
	}
	if f.HasNote {
		where = append(where, "note IS NOT NULL AND note != ''")
	}
	if f.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.id AND ft.tag = ?)")
		args = append(args, normalizeTag(f.Tag))
	}
	if len(f.FlowIDs) > 0 {
		ph := make([]string, len(f.FlowIDs))
		for i, id := range f.FlowIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id IN ("+strings.Join(ph, ",")+")")
	}
	return where, args
}
