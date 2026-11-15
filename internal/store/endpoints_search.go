package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
		eps, err := s.queryEndpointsAggregate(f, "", scope, nil)
		return eps, "", err
	}
	switch scope {
	case EndpointSearchHeaders:
		eps, err := s.queryEndpointsAggregate(f, term, scope, nil)
		return eps, "", err
	case EndpointSearchBody:
		keys, note, err := s.endpointKeysBodySearch(f)
		if err != nil {
			return nil, "", err
		}
		if len(keys) == 0 {
			return nil, note, nil
		}
		base := f
		base.Search = ""
		eps, err := s.queryEndpointsAggregate(base, "", EndpointSearchPath, keys)
		return eps, note, err
	case EndpointSearchAll:
		keys, note, err := s.endpointKeysAllSearch(f)
		if err != nil {
			return nil, "", err
		}
		if len(keys) == 0 {
			return nil, note, nil
		}
		base := f
		base.Search = ""
		eps, err := s.queryEndpointsAggregate(base, "", EndpointSearchPath, keys)
		return eps, note, err
	default:
		eps, err := s.queryEndpointsAggregate(f, term, EndpointSearchPath, nil)
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

func (s *Store) queryEndpointsAggregate(f EndpointFilter, term, scope string, onlyKeys map[endpointKey]struct{}) ([]Endpoint, error) {
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
	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}
	having := ""
	if f.HideNoiseOnly {
		having = ` HAVING MAX(CASE WHEN status NOT IN (403, 404) AND status > 0 THEN status ELSE 0 END) > 0
		              OR MAX(COALESCE(status, 0)) = 0`
	}
	q := `WITH agg AS (
	      SELECT host, method, path,
	             MAX(id) AS last_id,
	             COUNT(*) AS hits,
	             GROUP_CONCAT(DISTINCT status) AS statuses
	      FROM flows` + whereClause + `
	      GROUP BY host, method, path` + having + `
	      )
	      SELECT a.host, a.method, a.path,
	             COALESCE(f.scheme, ''), COALESCE(f.port, 0), COALESCE(f.status, 0),
	             COALESCE(f.res_body_hash, ''), COALESCE(f.res_len, 0),
	             a.last_id, a.hits, COALESCE(a.statuses, '')
	      FROM agg a
	      JOIN flows f ON f.id = a.last_id
	      ORDER BY a.host, a.path, a.method`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Endpoint
	softIdx := map[string]int{}
	var softHashes []string
	for rows.Next() {
		var e Endpoint
		var statusCSV string
		if err := rows.Scan(&e.Host, &e.Method, &e.Path, &e.Scheme, &e.Port, &e.LastStatus, &e.ResBodyHash, &e.ResLen, &e.LastFlowID, &e.Hits, &statusCSV); err != nil {
			return nil, err
		}
		if onlyKeys != nil {
			if _, ok := onlyKeys[endpointKey{e.Host, e.Method, e.Path}]; !ok {
				continue
			}
		}
		e.Statuses = parseStatusCSV(statusCSV)
		if e.LastStatus >= 200 && e.LastStatus < 400 && e.ResBodyHash != "" {
			if _, seen := softIdx[e.ResBodyHash]; !seen {
				softIdx[e.ResBodyHash] = len(softHashes)
				softHashes = append(softHashes, e.ResBodyHash)
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(softHashes) > 0 {
		softHits := s.checkSoft404Hashes(softHashes)
		for i := range out {
			if out[i].LastStatus >= 200 && out[i].LastStatus < 400 && out[i].ResBodyHash != "" {
				out[i].Soft404 = softHits[softIdx[out[i].ResBodyHash]]
			}
		}
	}
	return out, nil
}

func (s *Store) checkSoft404Hashes(hashes []string) []bool {
	hits := make([]bool, len(hashes))
	workers := min(bodySearchWorkers, len(hashes))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rawBuf []byte
			for {
				i := int(next.Add(1) - 1)
				if i >= len(hashes) {
					return
				}
				var data []byte
				var err error
				data, rawBuf, err = s.readRawBodyPrefixInto(hashes[i], maxEndpointBodyReadBytes, rawBuf)
				if err == nil {
					hits[i] = isSoft404Mutable(data)
				}
			}
		}()
	}
	wg.Wait()
	return hits
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
	q := `SELECT host, method, path, COALESCE(req_body_hash, ''), COALESCE(res_body_hash, '') FROM flows`
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

	type rowRefs struct {
		key    endpointKey
		reqIdx int
		resIdx int
	}
	hashIdx := map[string]int{}
	var hashes []string
	addHash := func(h string) int {
		if h == "" {
			return -1
		}
		if idx, seen := hashIdx[h]; seen {
			return idx
		}
		idx := len(hashes)
		hashIdx[h] = idx
		hashes = append(hashes, h)
		return idx
	}

	var candidates []rowRefs
	scanned := 0
	for rows.Next() {
		scanned++
		var host, method, path, reqH, resH sql.NullString
		if err := rows.Scan(&host, &method, &path, &reqH, &resH); err != nil {
			return nil, "", err
		}
		rIdxReq := addHash(reqH.String)
		rIdxRes := addHash(resH.String)
		if rIdxReq < 0 && rIdxRes < 0 {
			continue
		}
		candidates = append(candidates, rowRefs{
			key:    endpointKey{host: host.String, method: method.String, path: path.String},
			reqIdx: rIdxReq,
			resIdx: rIdxRes,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	rows.Close()

	keys := map[endpointKey]struct{}{}
	if len(hashes) > 0 {
		hits := s.matchRawBodyTerm(hashes, term, maxEndpointBodyReadBytes)
		for _, c := range candidates {
			if (c.reqIdx >= 0 && hits[c.reqIdx]) || (c.resIdx >= 0 && hits[c.resIdx]) {
				keys[c.key] = struct{}{}
			}
		}
	}
	var note string
	if scanned >= maxEndpointBodyScanFlows {
		note = fmt.Sprintf("Body search scanned the latest %d flows (limit). Narrow by domain or query if results look incomplete.", maxEndpointBodyScanFlows)
	}
	return keys, note, nil
}

func (s *Store) matchRawBodyTerm(hashes []string, lowerTerm string, maxBytes int64) []bool {
	hits := make([]bool, len(hashes))
	matcher := prepareBodyMatcher([]string{lowerTerm})
	workers := min(bodySearchWorkers, len(hashes))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rawBuf []byte
			var one [1]bool
			for {
				i := int(next.Add(1) - 1)
				if i >= len(hashes) {
					return
				}
				var data []byte
				var err error
				data, rawBuf, err = s.readRawBodyPrefixInto(hashes[i], maxBytes, rawBuf)
				if err == nil {
					one[0] = false
					matcher.matchInto(data, one[:])
					hits[i] = one[0]
				}
			}
		}()
	}
	wg.Wait()
	return hits
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
