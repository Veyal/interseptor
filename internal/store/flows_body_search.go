package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Veyal/interseptor/internal/codec"
)

const maxFlowBodyScanFlows = 8000

// FlowIDsBodySearch returns flow ids whose request or response body contains Search
// case-insensitively. Other FlowFilter fields apply before candidate selection, and
// so does the page cursor when History is sorted by id, so a later page searches the
// bodies of older flows instead of re-reading the newest ones. Content-encoded
// bodies (gzip, br, deflate, zstd) are decoded before matching. Body reads are
// bounded and cached by hash.
func (s *Store) FlowIDsBodySearch(f FlowFilter, maxScan int) ([]int64, string, error) {
	term := strings.ToLower(strings.TrimSpace(f.Search))
	if term == "" {
		return nil, "", nil
	}
	base := f
	base.Search = ""
	ids, capped, err := s.flowIDsBodyScan(base, term, maxScan, false)
	if err != nil || !capped {
		return ids, "", err
	}
	return ids, fmt.Sprintf("Body search covered %s %d filtered flows. Narrow with host/method filters if results look incomplete.", bodyScanWindowLabel(f), maxScanOrDefault(maxScan)), nil
}

// AnywhereSearchFilter rewrites an anywhere-scoped search into SQL the history list
// can page over. Stored fields (URL, method, MIME, client, error, note), request and
// response headers, and tags are matched in SQL across every filtered flow; bodies
// are read only for flows those fields do not already match, newest first (or from
// the page cursor when sorted by id), up to maxScan flows. The returned filter has
// Search cleared and carries AnyText/AnyTextIDs; note is non-empty when the body
// scan reached its bound.
func (s *Store) AnywhereSearchFilter(f FlowFilter, maxScan int) (FlowFilter, string, error) {
	term := strings.ToLower(strings.TrimSpace(f.Search))
	out := f
	out.Search = ""
	if term == "" {
		return out, "", nil
	}
	ids, capped, err := s.flowIDsBodyScan(out, term, maxScan, true)
	if err != nil {
		return out, "", err
	}
	out.AnyText = term
	out.AnyTextIDs = ids
	if !capped {
		return out, "", nil
	}
	return out, fmt.Sprintf("Bodies were searched in %s %d filtered flows; URLs, headers, notes and tags were searched in all of History.", bodyScanWindowLabel(f), maxScanOrDefault(maxScan)), nil
}

func maxScanOrDefault(maxScan int) int {
	if maxScan <= 0 {
		return maxFlowBodyScanFlows
	}
	return maxScan
}

func bodyScanWindowLabel(f FlowFilter) string {
	if bodyScanFollowsCursor(f) && flowPageCursorID(f) > 0 {
		return "the next"
	}
	if key, dir := normalizedFlowSort(f); key == "id" && dir > 0 {
		return "the oldest"
	}
	return "the latest"
}

// bodyScanFollowsCursor reports whether the bounded body window can follow the
// History page order. Only the id order can: for other sort keys the matching
// rows are spread over the whole history, so the window stays on the newest flows.
func bodyScanFollowsCursor(f FlowFilter) bool {
	key, _ := normalizedFlowSort(f)
	return key == "id"
}

// flowIDsBodyScan reads the bodies of at most maxScan filtered flows and returns the
// ids whose request or response body contains term (already lower-cased). With
// skipText, rows the AnyText SQL predicate already matches are not read, and the Go
// metadata check catches matches SQLite's ASCII-only lower() cannot fold.
func (s *Store) flowIDsBodyScan(f FlowFilter, term string, maxScan int, skipText bool) ([]int64, bool, error) {
	maxScan = maxScanOrDefault(maxScan)
	base := f
	base.AnyText, base.AnyTextIDs = "", nil
	where, filterArgs := buildFlowFilterWhere(base)
	args := append([]any(nil), filterArgs...)
	if skipText {
		pred, predArgs := anyTextPredicate(term, nil)
		where = append(where, "NOT "+pred)
		args = append(args, predArgs...)
	}
	order := " ORDER BY id DESC"
	if bodyScanFollowsCursor(f) {
		where, args = appendFlowPageCursor(f, where, args)
		if _, dir := normalizedFlowSort(f); dir > 0 {
			order = " ORDER BY id ASC"
		}
	}
	q := `SELECT id, scheme, host, path, method, mime, client_addr, error, note,
		req_headers, res_headers, req_body_hash, res_body_hash FROM flows`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += order + " LIMIT ?"
	args = append(args, maxScan)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	type bodyRef struct{ hash, enc string }
	type candidate struct {
		id   int64
		refs [2]bodyRef
	}
	var ids []int64
	var candidates []candidate
	unique := map[bodyRef]int{} // body -> index into keys
	var keys []bodyRef
	scanned := 0
	for rows.Next() {
		scanned++
		var id int64
		var scheme, host, path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders, reqH, resH string
		if err := rows.Scan(&id, &scheme, &host, &path, &method, &mime, &clientAddr, &flowErr, &note, &reqHeaders, &resHeaders, &reqH, &resH); err != nil {
			return nil, false, err
		}
		if skipText && anyFieldContains(term, scheme+"://"+host+path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders) {
			ids = append(ids, id)
			continue
		}
		c := candidate{id: id}
		for i, side := range [2][2]string{{reqH, reqHeaders}, {resH, resHeaders}} {
			if side[0] == "" {
				continue
			}
			ref := bodyRef{side[0], headerJSONContentEncoding(side[1])}
			c.refs[i] = ref
			if _, seen := unique[ref]; !seen {
				unique[ref] = len(keys)
				keys = append(keys, ref)
			}
		}
		if c.refs[0].hash != "" || c.refs[1].hash != "" {
			candidates = append(candidates, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	rows.Close()

	// Body reads dominate the cost (one file per distinct body), so they run on a
	// small worker pool; each distinct body is read once per search.
	hits := make([]bool, len(keys))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(bodySearchWorkers, len(keys)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				hit, err := s.encodedBodyContainsTerm(keys[i].hash, keys[i].enc, term, maxEndpointBodyReadBytes)
				hits[i] = err == nil && hit
			}
		}()
	}
	for i := range keys {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, c := range candidates {
		for _, ref := range c.refs {
			if ref.hash != "" && hits[unique[ref]] {
				ids = append(ids, c.id)
				break
			}
		}
	}
	return ids, scanned == maxScan, nil
}

// bodySearchWorkers bounds concurrent body-file reads during one History search.
const bodySearchWorkers = 8

// anyFieldContains mirrors anyTextPredicate per field with Unicode case folding.
func anyFieldContains(term string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), term) {
			return true
		}
	}
	return false
}

// headerJSONContentEncoding returns the Content-Encoding value from a stored header
// JSON object, or "" when absent. The cheap substring check keeps the common
// uncompressed row free of JSON decoding.
func headerJSONContentEncoding(raw string) string {
	if raw == "" || !strings.Contains(strings.ToLower(raw), "content-encoding") {
		return ""
	}
	var hdr map[string][]string
	if json.Unmarshal([]byte(raw), &hdr) != nil {
		return ""
	}
	for k, v := range hdr {
		if strings.EqualFold(k, "Content-Encoding") && len(v) > 0 {
			enc := strings.ToLower(strings.TrimSpace(strings.Join(v, ",")))
			if enc == "identity" {
				return ""
			}
			return enc
		}
	}
	return ""
}

// encodedBodyContainsTerm decodes a content-encoded body before matching, so a
// gzip or brotli response captured from a browser is searchable like the inspector
// shows it. A body that does not decode is matched as stored.
func (s *Store) encodedBodyContainsTerm(hash, enc, term string, maxBytes int64) (bool, error) {
	if enc == "" {
		return s.bodyContainsTerm(hash, term, maxBytes)
	}
	rc, err := s.OpenBody(hash)
	if err != nil {
		return false, err
	}
	data, ok, _ := codec.DecompressReaderLimit(enc, rc, int(maxBytes))
	_ = rc.Close()
	if !ok {
		return s.bodyContainsTerm(hash, term, maxBytes)
	}
	return strings.Contains(strings.ToLower(string(data)), term), nil
}

// anyTextPredicate is the SQL half of an anywhere search: the term (lower-cased)
// against the stored URL, method, MIME, client, error, note, header JSON and tags,
// OR'ed with the body-hit ids found by the bounded body scan.
func anyTextPredicate(term string, ids []int64) (string, []any) {
	parts := []string{
		"instr(lower(scheme || '://' || host || path), ?) > 0",
		"instr(lower(method), ?) > 0",
		"instr(lower(COALESCE(mime, '')), ?) > 0",
		"instr(lower(COALESCE(client_addr, '')), ?) > 0",
		"instr(lower(COALESCE(error, '')), ?) > 0",
		"instr(lower(COALESCE(note, '')), ?) > 0",
		"instr(lower(COALESCE(req_headers, '')), ?) > 0",
		"instr(lower(COALESCE(res_headers, '')), ?) > 0",
		"EXISTS (SELECT 1 FROM flow_tags ft WHERE ft.flow_id = flows.id AND instr(lower(ft.tag), ?) > 0)",
	}
	args := make([]any, 0, len(parts)+len(ids))
	for range parts {
		args = append(args, term)
	}
	if len(ids) > 0 {
		ph := make([]string, len(ids))
		for i, id := range ids {
			ph[i] = "?"
			args = append(args, id)
		}
		parts = append(parts, "id IN ("+strings.Join(ph, ",")+")")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}
