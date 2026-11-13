package store

import (
	"strconv"
	"strings"
	"time"
)

// flowListColumns is the slim SELECT for history lists — no header JSON blobs.
const flowListColumns = `id, ts, method, scheme, host, port, path, http_version, status,
	req_len, res_len, mime, duration_ms, client_addr, error, flags, note`

// QueryFlowsListFilter is like QueryFlowsFilter but skips req/res header columns — use for
// list/summary views that only need flowJSON fields; use QueryFlowsFilter when headers or body
// hashes are needed downstream.
func (s *Store) QueryFlowsListFilter(f FlowFilter) ([]*Flow, error) {
	s.syncFlows()
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q, args := s.flowListQuery(f, flowListColumns, limit)
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

// flowListQuery builds one page of the History list for f.
func (s *Store) flowListQuery(f FlowFilter, columns string, limit int) (string, []any) {
	if q, args, ok := ftsDrivenListQuery(f, columns, limit); ok {
		return q, args
	}
	where, args := buildFlowFilterWhere(f)
	if s.statusClassIsCommon(f) {
		for i, w := range where {
			if w == statusClassPredicate {
				where[i] = statusClassScanPredicate
			}
		}
	}
	where, args = appendFlowPageCursor(f, where, args)
	q := "SELECT " + columns + " FROM flows"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	return q + flowListOrderBy(f) + " LIMIT " + strconv.Itoa(limit), args
}

const (
	statusClassPredicate = "status >= ? AND status < ?"
	// The unary plus keeps the planner off idx_flows_status, so an id-ordered
	// list walks flows newest first and stops at a full page.
	statusClassScanPredicate = "+status >= ? AND +status < ?"
)

// statusClassScanThreshold is the number of flows in a status class below which
// the status index is always the cheaper plan. A var so tests can lower it.
var statusClassScanThreshold = 2000

// statusClassIsCommon reports whether an id-ordered list filtered by status
// class should walk flows in id order instead of using idx_flows_status. The
// index plan reads every flow of the class and sorts them before returning the
// first page, so it costs as much as the class is large (about 4 µs a flow:
// 0.96 s for "2xx" on 500,000 flows); the walk stops at a full page but, for a
// class with no recent flow, reads the whole table (about 0.28 µs a flow). A
// class holding at least a sixteenth of History is therefore where even the
// worst-case walk costs no more than the index plan, and that is the
// cut-over. The probe counts through the covering status index and stops at
// the threshold.
func (s *Store) statusClassIsCommon(f FlowFilter) bool {
	if f.StatusClass < 1 || f.StatusClass > 5 {
		return false
	}
	if key, _ := normalizedFlowSort(f); key != "id" {
		return false
	}
	var maxID int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM flows`).Scan(&maxID); err != nil {
		return false
	}
	threshold := max(int64(statusClassScanThreshold), maxID/16)
	lo := f.StatusClass * 100
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM flows WHERE status >= ? AND status < ? LIMIT ?)`,
		lo, lo+100, threshold).Scan(&n)
	return err == nil && n >= threshold
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
	if f.EndpointScheme != "" {
		where = append(where, "scheme = ?")
		args = append(args, f.EndpointScheme)
	}
	if f.EndpointHost != "" {
		where = append(where, "lower(host) = lower(?)")
		args = append(args, f.EndpointHost)
	}
	if f.EndpointPort > 0 {
		where = append(where, "port = ?")
		args = append(args, f.EndpointPort)
	}
	if f.EndpointPath != "" {
		where = append(where, "CASE WHEN instr(path, '?') > 0 THEN substr(path, 1, instr(path, '?') - 1) ELSE path END = ?")
		args = append(args, f.EndpointPath)
	}
	// "-term" tokens are exclusions, never text to match; a caller may also name
	// exclusions directly in NotTerms (the search rewriter does).
	search, notFromSearch := SplitNegativeSearch(f.Search)
	if search != "" {
		if id, ok := flowSearchAsID(search, f.SearchScope); ok {
			where = append(where, "id = ?")
			args = append(args, id)
		} else {
			where, args = appendFTSSearch(where, args, search)
		}
	}
	for _, t := range append(notFromSearch, f.NotTerms...) {
		pred, predArgs := anyTextPredicate(strings.ToLower(t), nil)
		where = append(where, "NOT "+pred)
		args = append(args, predArgs...)
	}
	if f.StatusClass >= 1 && f.StatusClass <= 5 {
		lo := f.StatusClass * 100
		where = append(where, statusClassPredicate)
		args = append(args, lo, lo+100)
	}
	if f.RequireFlags != 0 {
		where = append(where, flagsAnyPredicate(f.RequireFlags))
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
	if f.AnyText != "" {
		pred, predArgs := anyTextPredicate(f.AnyText, f.AnyTextIDs)
		where = append(where, pred)
		args = append(args, predArgs...)
	}
	if f.ScanBoundID > 0 {
		if _, dir := normalizedFlowSort(f); dir > 0 {
			where = append(where, "id <= ?")
		} else {
			where = append(where, "id >= ?")
		}
		args = append(args, f.ScanBoundID)
	}
	if f.HideBrowserNoise {
		pred, predArgs := browserNoisePredicate()
		where = append(where, "NOT "+pred)
		args = append(args, predArgs...)
	}
	return where, args
}
