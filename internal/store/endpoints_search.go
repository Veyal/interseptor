package store

import (
	"fmt"
	"io"
	"strings"
)

const (
	maxEndpointBodyScanFlows = 8000
	maxEndpointBodyReadBytes = 256 << 10 // 256 KiB per body — enough for most HTML/JSON
)

type endpointKey struct {
	host, method, path string
}

// Endpoints returns unique endpoints aggregated from flows. When SearchScope is
// headers, body, or all, Search filters by stored headers and/or body content
// (body search is bounded — see maxEndpointBodyScanFlows).
func (s *Store) Endpoints(f EndpointFilter) ([]Endpoint, string, error) {
	scope := normalizeEndpointSearchScope(f.SearchScope)
	term := strings.TrimSpace(f.Search)
	if term == "" {
		eps, err := s.queryEndpointsAggregate(f, "", scope)
		return eps, "", err
	}
	switch scope {
	case EndpointSearchHeaders:
		eps, err := s.queryEndpointsAggregate(f, term, scope)
		return eps, "", err
	case EndpointSearchBody:
		keys, note, err := s.endpointKeysBodySearch(f)
		if err != nil {
			return nil, "", err
		}
		base := f
		base.Search = ""
		eps, err := s.queryEndpointsAggregate(base, "", EndpointSearchPath)
		if err != nil {
			return nil, "", err
		}
		return filterEndpointsByKeys(eps, keys), note, nil
	case EndpointSearchAll:
		keys, note, err := s.endpointKeysAllSearch(f)
		if err != nil {
			return nil, "", err
		}
		base := f
		base.Search = ""
		eps, err := s.queryEndpointsAggregate(base, "", EndpointSearchPath)
		if err != nil {
			return nil, "", err
		}
		return filterEndpointsByKeys(eps, keys), note, nil
	default:
		eps, err := s.queryEndpointsAggregate(f, term, EndpointSearchPath)
		return eps, "", err
	}
}

func normalizeEndpointSearchScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case EndpointSearchHeaders, EndpointSearchBody, EndpointSearchAll:
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return EndpointSearchPath
	}
}

func (s *Store) queryEndpointsAggregate(f EndpointFilter, term, scope string) ([]Endpoint, error) {
	where, args := endpointBaseWhere(f)
	term = strings.TrimSpace(term)
	if term != "" {
		switch scope {
		case EndpointSearchHeaders:
			where = append(where, "(instr(lower(req_headers), lower(?)) > 0 OR instr(lower(res_headers), lower(?)) > 0)")
			args = append(args, term, term)
		default: // path — host, path, method
			where = append(where, "(instr(lower(path), lower(?)) > 0 OR instr(lower(host), lower(?)) > 0 OR instr(lower(method), lower(?)) > 0)")
			args = append(args, term, term, term)
		}
	}
	q := `SELECT host, method, path, scheme, status, MAX(id) AS last_id, COUNT(*) AS hits,
	             GROUP_CONCAT(DISTINCT status) AS statuses
	      FROM flows`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " GROUP BY host, method, path ORDER BY host, path, method"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		var statusCSV string
		if err := rows.Scan(&e.Host, &e.Method, &e.Path, &e.Scheme, &e.LastStatus, &e.LastFlowID, &e.Hits, &statusCSV); err != nil {
			return nil, err
		}
		e.Statuses = parseStatusCSV(statusCSV)
		out = append(out, e)
	}
	return out, rows.Err()
}

func endpointBaseWhere(f EndpointFilter) ([]string, []any) {
	var where []string
	var args []any
	if f.ExcludeFlags != 0 {
		where = append(where, "(flags & ?) = 0")
		args = append(args, f.ExcludeFlags)
	}
	if f.Host != "" {
		where = append(where, "instr(lower(host), lower(?)) > 0")
		args = append(args, f.Host)
	}
	if f.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.id AND ft.tag = ?)")
		args = append(args, normalizeTag(f.Tag))
	}
	return where, args
}

func (s *Store) endpointKeysBodySearch(f EndpointFilter) (map[endpointKey]struct{}, string, error) {
	term := strings.ToLower(strings.TrimSpace(f.Search))
	if term == "" {
		return nil, "", nil
	}
	where, args := endpointBaseWhere(f)
	q := `SELECT host, method, path, req_body_hash, res_body_hash FROM flows`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, maxEndpointBodyScanFlows)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	hashHit := map[string]bool{}
	keys := map[endpointKey]struct{}{}
	scanned := 0
	for rows.Next() {
		scanned++
		var host, method, path, reqH, resH string
		if err := rows.Scan(&host, &method, &path, &reqH, &resH); err != nil {
			return nil, "", err
		}
		k := endpointKey{host: host, method: method, path: path}
		for _, hash := range []string{reqH, resH} {
			if hash == "" {
				continue
			}
			hit, ok := hashHit[hash]
			if !ok {
				var err error
				hit, err = s.bodyContainsTerm(hash, term, maxEndpointBodyReadBytes)
				if err != nil {
					hit = false
				}
				hashHit[hash] = hit
			}
			if hit {
				keys[k] = struct{}{}
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var note string
	if scanned >= maxEndpointBodyScanFlows {
		note = fmt.Sprintf("Body search scanned the latest %d flows (limit). Narrow by domain or query if results look incomplete.", maxEndpointBodyScanFlows)
	}
	return keys, note, nil
}

func (s *Store) endpointKeysAllSearch(f EndpointFilter) (map[endpointKey]struct{}, string, error) {
	term := strings.TrimSpace(f.Search)
	if term == "" {
		return nil, "", nil
	}
	keys := map[endpointKey]struct{}{}

	where, args := endpointBaseWhere(f)
	where = append(where, `(instr(lower(path), lower(?)) > 0 OR instr(lower(host), lower(?)) > 0 OR instr(lower(method), lower(?)) > 0
		OR instr(lower(req_headers), lower(?)) > 0 OR instr(lower(res_headers), lower(?)) > 0)`)
	args = append(args, term, term, term, term, term)
	q := `SELECT DISTINCT host, method, path FROM flows`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var k endpointKey
		if err := rows.Scan(&k.host, &k.method, &k.path); err != nil {
			rows.Close()
			return nil, "", err
		}
		keys[k] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", err
	}
	rows.Close()

	bodyKeys, note, err := s.endpointKeysBodySearch(f)
	if err != nil {
		return nil, "", err
	}
	for k := range bodyKeys {
		keys[k] = struct{}{}
	}
	return keys, note, nil
}

func (s *Store) bodyContainsTerm(hash, term string, maxBytes int64) (bool, error) {
	rc, err := s.OpenBody(hash)
	if err != nil {
		return false, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxBytes))
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(string(data)), strings.ToLower(term)), nil
}

func filterEndpointsByKeys(eps []Endpoint, keys map[endpointKey]struct{}) []Endpoint {
	if len(keys) == 0 {
		return nil
	}
	out := make([]Endpoint, 0, len(keys))
	for _, e := range eps {
		if _, ok := keys[endpointKey{e.Host, e.Method, e.Path}]; ok {
			out = append(out, e)
		}
	}
	return out
}
