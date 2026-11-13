package store

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Veyal/interseptor/internal/codec"
)

const maxFlowBodyScanFlows = 8000

// FlowIDsBodySearch returns flow ids whose request or response body contains Search
// case-insensitively, minus flows whose bodies contain one of its "-term"
// exclusions. Other FlowFilter fields apply before candidate selection, and so
// does the page cursor when History is sorted by id, so a later page searches the
// bodies of older flows instead of re-reading the newest ones. Content-encoded
// bodies (gzip, br, deflate, zstd) are decoded before matching. Body reads are
// bounded and cached by hash. bound is the id of the last flow the capped scan read
// when the window follows the id order (0 otherwise): rows past it were not
// searched, so the caller pages on from there (FlowFilter.ScanBoundID).
func (s *Store) FlowIDsBodySearch(f FlowFilter, maxScan int) (ids []int64, note string, bound int64, err error) {
	positive, excluded := SplitNegativeSearch(f.Search)
	term := strings.ToLower(strings.TrimSpace(positive))
	if term == "" {
		// Body scope matches bodies only: an exclusion with no term to include has
		// no candidates, and an id is not a body match. An anywhere search over
		// exclusions alone is handled by AnywhereSearchFilter instead.
		return nil, "", 0, nil
	}
	base := f
	base.Search = ""
	// Body scope excludes on body text only (the scan below), not on stored
	// fields, and carries no text predicate of its own.
	base.NotTerms = nil
	base.AnyText, base.AnyTextIDs = "", nil
	ids, capped, bound, err := s.flowIDsBodyScan(base, term, excluded, maxScan, false)
	if err != nil || !capped {
		return ids, "", 0, err
	}
	return ids, fmt.Sprintf("Body search covered %s %d filtered flows. Narrow with host/method filters if results look incomplete.", bodyScanWindowLabel(f), maxScanOrDefault(maxScan)), bound, nil
}

// AnywhereSearchFilter rewrites an anywhere-scoped search into SQL the history list
// can page over. Stored fields (URL, method, MIME, client, error, note), request and
// response headers, and tags are matched in SQL across every filtered flow; bodies
// are read only for flows those fields do not already match, newest first (or from
// the page cursor when sorted by id), up to maxScan flows. "-term" tokens in the
// search become exclusions of those same stored fields, so a term list can be
// narrowed ("-.jpg -analytics") without reading any body. The returned filter has
// Search cleared and carries AnyText/AnyTextIDs and NotTerms; note is non-empty
// when the body scan reached its bound. When that window follows the id order,
// ScanBoundID stops the page at the last scanned flow, so an older metadata match
// cannot be listed ahead of body matches that were never read; the next page
// continues from there.
func (s *Store) AnywhereSearchFilter(f FlowFilter, maxScan int) (FlowFilter, string, error) {
	positive, excluded := SplitNegativeSearch(f.Search)
	out := f
	out.Search = ""
	out.NotTerms = append(append([]string(nil), f.NotTerms...), excluded...)
	term := strings.ToLower(strings.TrimSpace(positive))
	if term == "" {
		// Exclusion-only searches still filter every stored surface in SQL; there
		// is nothing to look for in a body.
		return out, "", nil
	}
	// An id lookup (#123, id:123) is exact, not a text search: keep the term so
	// the list query resolves it as an id. Its exclusions still apply.
	if _, ok := flowSearchAsID(positive, ""); ok {
		out.Search = positive
		return out, "", nil
	}
	ids, capped, bound, err := s.flowIDsBodyScan(out, term, nil, maxScan, true)
	if err != nil {
		return out, "", err
	}
	out.AnyText = term
	out.AnyTextIDs = ids
	if !capped {
		return out, "", nil
	}
	out.ScanBoundID = bound
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
// ids whose request or response body contains term (already lower-cased) and none of
// the excluded terms. With skipText, rows the AnyText SQL predicate already matches
// are not re-read for the positive term, but they are still read when exclusions
// apply because only the body can drop them; the Go metadata check catches matches
// SQLite's ASCII-only lower() cannot fold.
func (s *Store) flowIDsBodyScan(f FlowFilter, term string, excluded []string, maxScan int, skipText bool) ([]int64, bool, int64, error) {
	if term == "" {
		return nil, false, 0, nil
	}
	maxScan = maxScanOrDefault(maxScan)
	base := f
	base.AnyText, base.AnyTextIDs, base.ScanBoundID = "", nil, 0
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
		return nil, false, 0, err
	}
	defer rows.Close()

	type bodyRef struct{ hash, enc string }
	type candidate struct {
		id      int64
		refs    [2]bodyRef
		textHit bool // matched a stored field, so the body only matters for exclusions
	}
	var ids []int64
	var candidates []candidate
	unique := map[bodyRef]int{} // body -> index into keys
	var keys []bodyRef
	scanned := 0
	var lastID int64
	for rows.Next() {
		scanned++
		var id int64
		var scheme, host, path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders, reqH, resH string
		if err := rows.Scan(&id, &scheme, &host, &path, &method, &mime, &clientAddr, &flowErr, &note, &reqHeaders, &resHeaders, &reqH, &resH); err != nil {
			return nil, false, 0, err
		}
		lastID = id
		c := candidate{id: id}
		if skipText && anyFieldContains(term, scheme+"://"+host+path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders) {
			c.textHit = true
		}
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
		if c.refs[0].hash == "" && c.refs[1].hash == "" {
			// Nothing stored to read: a stored-field match still counts, and an
			// exclusion was already applied against those same fields in SQL.
			if c.textHit {
				ids = append(ids, id)
			}
			continue
		}
		if c.textHit && len(excluded) == 0 {
			// The stored fields already matched and no exclusion can drop the row
			// on body text, so there is nothing a read would decide.
			ids = append(ids, id)
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, err
	}
	rows.Close()

	// Body reads dominate the cost (one file per distinct body), so they run on a
	// small worker pool; each distinct body is read once and checked against the
	// positive term and every exclusion.
	needles := append([]string{term}, excluded...)
	hits := make([][]bool, len(keys))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(bodySearchWorkers, len(keys)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				text, err := s.encodedBodyText(keys[i].hash, keys[i].enc, maxEndpointBodyReadBytes)
				keyHits := make([]bool, len(needles))
				if err == nil {
					for n, needle := range needles {
						keyHits[n] = strings.Contains(text, needle)
					}
				}
				hits[i] = keyHits
			}
		}()
	}
	for i := range keys {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, c := range candidates {
		matched, dropped := c.textHit, false
		for _, ref := range c.refs {
			if ref.hash == "" {
				continue
			}
			keyHits := hits[unique[ref]]
			matched = matched || keyHits[0]
			for _, negHit := range keyHits[1:] {
				dropped = dropped || negHit
			}
		}
		if matched && !dropped {
			ids = append(ids, c.id)
		}
	}
	capped := scanned == maxScan
	var bound int64
	if capped && bodyScanFollowsCursor(f) {
		bound = lastID
	}
	return ids, capped, bound, nil
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

// encodedBodyText decodes a content-encoded body before matching, so a gzip or
// brotli response captured from a browser is searchable like the inspector shows
// it. A body that does not decode is read as stored. The text is lower-cased once;
// every search term is matched against it.
func (s *Store) encodedBodyText(hash, enc string, maxBytes int64) (string, error) {
	if enc == "" {
		return s.bodyText(hash, maxBytes)
	}
	rc, err := s.OpenBody(hash)
	if err != nil {
		return "", err
	}
	data, ok, _ := codec.DecompressReaderLimit(enc, rc, int(maxBytes))
	_ = rc.Close()
	if !ok {
		return s.bodyText(hash, maxBytes)
	}
	return strings.ToLower(string(data)), nil
}

func (s *Store) bodyText(hash string, maxBytes int64) (string, error) {
	rc, err := s.OpenBody(hash)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxBytes))
	if err != nil {
		return "", err
	}
	return strings.ToLower(string(data)), nil
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
