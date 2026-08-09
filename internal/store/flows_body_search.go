package store

import (
	"fmt"
	"strings"
)

const maxFlowBodyScanFlows = 8000

// FlowIDsBodySearch returns flow ids whose request or response body contains Search
// case-insensitively. Other FlowFilter fields apply before candidate selection. Body
// reads are bounded and cached by hash.
func (s *Store) FlowIDsBodySearch(f FlowFilter, maxScan int) ([]int64, string, error) {
	return s.flowIDsSearch(f, maxScan, false)
}

// FlowIDsAnywhereSearch returns flow ids whose stored fields, tags, request headers,
// response headers, or bodies contain Search case-insensitively.
func (s *Store) FlowIDsAnywhereSearch(f FlowFilter, maxScan int) ([]int64, string, error) {
	return s.flowIDsSearch(f, maxScan, true)
}

func (s *Store) flowIDsSearch(f FlowFilter, maxScan int, anywhere bool) ([]int64, string, error) {
	term := strings.ToLower(strings.TrimSpace(f.Search))
	if term == "" {
		return nil, "", nil
	}
	if maxScan <= 0 {
		maxScan = maxFlowBodyScanFlows
	}
	base := f
	base.Search = ""
	where, filterArgs := buildFlowFilterWhere(base)
	q := `SELECT id, scheme, host, path, method, mime, client_addr, error, note,
		req_headers, res_headers, req_body_hash, res_body_hash,
		EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.id AND instr(lower(ft.tag), ?) > 0)
		FROM flows`
	args := append([]any{term}, filterArgs...)
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, maxScan)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	hashHit := map[string]bool{}
	var ids []int64
	scanned := 0
	for rows.Next() {
		scanned++
		var id int64
		var scheme, host, path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders, reqH, resH string
		var tagHit bool
		if err := rows.Scan(&id, &scheme, &host, &path, &method, &mime, &clientAddr, &flowErr, &note, &reqHeaders, &resHeaders, &reqH, &resH, &tagHit); err != nil {
			return nil, "", err
		}
		metadata := scheme + "://" + host + path + method + mime + clientAddr + flowErr + note + reqHeaders + resHeaders
		if anywhere && (strings.Contains(strings.ToLower(metadata), term) || tagHit) {
			ids = append(ids, id)
			continue
		}
		for _, hash := range []string{reqH, resH} {
			if hash == "" {
				continue
			}
			hit, ok := hashHit[hash]
			if !ok {
				var bodyErr error
				hit, bodyErr = s.bodyContainsTerm(hash, term, maxEndpointBodyReadBytes)
				if bodyErr != nil {
					hit = false
				}
				hashHit[hash] = hit
			}
			if hit {
				ids = append(ids, id)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var note string
	if scanned == maxScan {
		scope := "Body"
		if anywhere {
			scope = "Anywhere"
		}
		note = fmt.Sprintf("%s search scanned the latest %d filtered flows. Narrow with host/method filters if results look incomplete.", scope, maxScan)
	}
	return ids, note, nil
}
