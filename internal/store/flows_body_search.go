package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

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

	// Only U+0130 ('İ' -> 'i') and U+212A ('K' -> 'k') fold from non-ASCII to
	// ASCII under Go's strings.ToLower, so pure-ASCII queries without 'i' or 'k'
	// are already answered exhaustively by SQLite's ASCII lower() above and need
	// neither the 8 metadata columns nor Go-side lowercasing.
	termHigh := hasHighByteString(term)
	needMetaCheck := skipText && (termHigh || strings.ContainsAny(term, "ik"))
	selectCols := `id, req_headers, res_headers, req_body_hash, res_body_hash`
	if needMetaCheck {
		selectCols = `id, scheme, host, path, method, mime, client_addr, error, note,
		req_headers, res_headers, req_body_hash, res_body_hash`
	}
	q := `SELECT ` + selectCols + ` FROM flows`
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
		var scheme, host, path, method, mime, clientAddr, flowErr, note, reqHeaders, resHeaders, reqH, resH sql.NullString
		if needMetaCheck {
			err = rows.Scan(&id, &scheme, &host, &path, &method, &mime, &clientAddr, &flowErr, &note, &reqHeaders, &resHeaders, &reqH, &resH)
		} else {
			err = rows.Scan(&id, &reqHeaders, &resHeaders, &reqH, &resH)
		}
		if err != nil {
			return nil, false, 0, err
		}
		lastID = id
		c := candidate{id: id}
		if needMetaCheck && (termHigh || anyStringHasHighByte(scheme.String, host.String, path.String, method.String, mime.String, clientAddr.String, flowErr.String, note.String, reqHeaders.String, resHeaders.String)) {
			if anyFieldContains(term, scheme.String+"://"+host.String+path.String, method.String, mime.String, clientAddr.String, flowErr.String, note.String, reqHeaders.String, resHeaders.String) {
				c.textHit = true
			}
		}
		for i, side := range [2][2]string{{reqH.String, reqHeaders.String}, {resH.String, resHeaders.String}} {
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
			if c.textHit {
				ids = append(ids, id)
			}
			continue
		}
		if c.textHit && len(excluded) == 0 {
			ids = append(ids, id)
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, err
	}
	rows.Close()

	needles := make([]string, 1+len(excluded))
	needles[0] = term
	copy(needles[1:], excluded)
	stride := len(needles)
	hits := make([]bool, len(keys)*stride)
	matcher := prepareBodyMatcher(needles)

	// Body reads dominate the cost (one file per distinct body), so they run on a
	// bounded worker pool; each worker reuses scratch buffers to keep GC flat.
	workers := min(bodySearchWorkers, len(keys))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rawBuf, decBuf []byte
			for {
				i := int(next.Add(1) - 1)
				if i >= len(keys) {
					return
				}
				var data []byte
				var err error
				data, rawBuf, decBuf, err = s.readEncodedBodyPrefixInto(keys[i].hash, keys[i].enc, maxEndpointBodyReadBytes, rawBuf, decBuf)
				if err != nil {
					continue
				}
				matcher.matchInto(data, hits[i*stride:(i+1)*stride])
			}
		}()
	}
	wg.Wait()

	for _, c := range candidates {
		matched, dropped := c.textHit, false
		for _, ref := range c.refs {
			if ref.hash == "" {
				continue
			}
			baseIdx := unique[ref] * stride
			matched = matched || hits[baseIdx]
			for j := 1; j < stride; j++ {
				dropped = dropped || hits[baseIdx+j]
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

type bodyNeedleMatcher struct {
	needles     [][]byte
	hasHighByte bool
	hasIK       bool
}

func prepareBodyMatcher(lowerNeedles []string) bodyNeedleMatcher {
	m := bodyNeedleMatcher{needles: make([][]byte, len(lowerNeedles))}
	for i, s := range lowerNeedles {
		b := []byte(s)
		m.needles[i] = b
		if hasHighByteString(s) {
			m.hasHighByte = true
		}
		if !m.hasIK && strings.ContainsAny(s, "ik") {
			m.hasIK = true
		}
	}
	return m
}

// matchInto searches data (which may be modified in place) for each needle and
// records the boolean hits in dst. Pure-ASCII needles without 'i'/'k', as well
// as non-IK needles in pure-ASCII bodies, run completely allocation-free.
func (m bodyNeedleMatcher) matchInto(data []byte, dst []bool) {
	if len(data) == 0 {
		return
	}
	haystack := data
	if m.hasHighByte {
		haystack = bytes.ToLower(data)
	} else {
		hasHigh := foldASCIIInPlace(data)
		if hasHigh && m.hasIK {
			haystack = bytes.ToLower(data)
		}
	}
	for i, n := range m.needles {
		if len(n) > 0 && bytes.Contains(haystack, n) {
			dst[i] = true
		}
	}
}

func foldASCIIInPlace(b []byte) bool {
	var high byte
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
		high |= c
	}
	return high&0x80 != 0
}

func hasHighByteString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i]&0x80 != 0 {
			return true
		}
	}
	return false
}

func anyStringHasHighByte(fields ...string) bool {
	for _, f := range fields {
		if hasHighByteString(f) {
			return true
		}
	}
	return false
}

func containsASCIIFold(s, lowerSub string) bool {
	if len(lowerSub) == 0 {
		return true
	}
	if len(s) < len(lowerSub) {
		return false
	}
	limit := len(s) - len(lowerSub)
	for i := 0; i <= limit; i++ {
		matched := true
		for j := 0; j < len(lowerSub); j++ {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerSub[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

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
// JSON object, or "" when absent. The zero-alloc substring check keeps the common
// uncompressed row free of JSON decoding and string copies.
func headerJSONContentEncoding(raw string) string {
	if raw == "" || !containsASCIIFold(raw, "content-encoding") {
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

func (s *Store) readEncodedBodyPrefixInto(hash, enc string, maxBytes int64, rawBuf, decBuf []byte) (data, nextRaw, nextDec []byte, err error) {
	if enc != "" {
		rc, err := s.OpenBody(hash)
		if err != nil {
			return nil, rawBuf, decBuf, err
		}
		dec, ok, _ := codec.DecompressReaderLimitInto(enc, rc, int(maxBytes), decBuf)
		_ = rc.Close()
		if ok {
			if cap(dec) > cap(decBuf) {
				decBuf = dec[:0]
			}
			return dec, rawBuf, decBuf, nil
		}
	}
	raw, nextRaw, err := s.readRawBodyPrefixInto(hash, maxBytes, rawBuf)
	return raw, nextRaw, decBuf, err
}

func (s *Store) readRawBodyPrefixInto(hash string, maxBytes int64, dst []byte) (data, nextDst []byte, err error) {
	rc, err := s.OpenBody(hash)
	if err != nil {
		return nil, dst, err
	}
	defer rc.Close()
	buf := dst[:0]
	var sizeHint int
	if sf, ok := rc.(interface{ Stat() (os.FileInfo, error) }); ok {
		if fi, statErr := sf.Stat(); statErr == nil && fi.Size() >= 0 {
			n := fi.Size()
			if n > maxBytes {
				n = maxBytes
			}
			sizeHint = int(n)
		}
	}
	if sizeHint > 0 && cap(buf) < sizeHint {
		buf = make([]byte, 0, sizeHint)
	} else if cap(buf) == 0 {
		buf = make([]byte, 0, 4096)
	}
	limit := int(maxBytes)
	for len(buf) < limit {
		if len(buf) == cap(buf) {
			nextCap := min(cap(buf)*2, limit)
			if nextCap <= cap(buf) {
				nextCap = limit
			}
			grown := make([]byte, len(buf), nextCap)
			copy(grown, buf)
			buf = grown
		}
		n, readErr := rc.Read(buf[len(buf):min(cap(buf), limit)])
		buf = buf[:len(buf)+n]
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, buf[:0], readErr
		}
	}
	return buf, buf[:0], nil
}

// anyTextPredicate is the SQL half of an anywhere search: the term (lower-cased)
// against the stored URL, method, MIME, client, error, note, header JSON and tags,
// OR'ed with the body-hit ids found by the bounded body scan.
func anyTextPredicate(term string, ids []int64) (string, []any) {
	parts := []string{
		"instr(lower(COALESCE(scheme, '') || '://' || COALESCE(host, '') || COALESCE(path, '')), ?) > 0",
		"instr(lower(COALESCE(method, '')), ?) > 0",
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
