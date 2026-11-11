package control

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/store"
)

// Flow slices: a bounded, agent-friendly view of one side of a flow.
//
// GET /api/flows/{id}/raw returns the whole message, which an MCP client
// cannot take for multi-megabyte bodies (the bridge caps a reply at 4 MiB and
// then returned nothing at all, not even the headers) and which dumps binary
// bodies as mojibake. The slice endpoint always returns the head (start line
// and headers), describes the body (size, MIME, binary or text) and returns
// either one byte window of a text body or regex matches with context lines.

const (
	sliceDefaultLimit   = 4000
	sliceMaxLimit       = 1 << 20
	sliceSniffBytes     = 8 << 10
	sliceHexHeadBytes   = 256
	sliceMaxPattern     = 1000
	sliceMaxContext     = 10
	sliceDefaultMatches = 20
	sliceMaxMatches     = 200
	sliceMaxLineRunes   = 300
)

type sliceMatch struct {
	Line   int    `json:"line"`   // 1-based line number of the matching line
	Offset int    `json:"offset"` // byte offset of the match in the (decoded) body
	Text   string `json:"text"`   // matching line with context lines around it
}

type sliceGrep struct {
	Pattern   string       `json:"pattern"`
	Count     int          `json:"count"` // matching lines found (capped by the scan)
	Matches   []sliceMatch `json:"matches"`
	Truncated bool         `json:"truncated,omitempty"`
}

type flowSliceJSON struct {
	ID        int64      `json:"id"`
	Side      string     `json:"side"`
	Head      string     `json:"head"`
	Mime      string     `json:"mime,omitempty"`
	BodyBytes int64      `json:"bodyBytes"`
	BodyHash  string     `json:"bodyHash,omitempty"`
	Binary    bool       `json:"binary"`
	HexHead   string     `json:"hexHead,omitempty"`
	Offset    int        `json:"offset"`
	Text      string     `json:"text,omitempty"`
	Shown     int        `json:"shown"`
	More      bool       `json:"more,omitempty"`
	Next      int        `json:"nextOffset,omitempty"`
	Decoded   bool       `json:"decoded,omitempty"`   // Content-Encoding was removed for display
	Capped    bool       `json:"decodeCap,omitempty"` // decoded body exceeded the display cap; sizes are of the capped body
	Grep      *sliceGrep `json:"grep,omitempty"`
}

func (h *flowAPI) getFlowSlice(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	side := "req"
	if s := strings.ToLower(q.Get("side")); s == "res" || s == "response" {
		side = "res"
	}
	offset, err := sliceInt(q.Get("offset"), 0, 0, 1<<40)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "offset must be a non-negative integer")
		return
	}
	limit, err := sliceInt(q.Get("limit"), sliceDefaultLimit, 0, sliceMaxLimit)
	if err != nil {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 0 and %d", sliceMaxLimit))
		return
	}
	var re *regexp.Regexp
	if pat := q.Get("grep"); pat != "" {
		if len(pat) > sliceMaxPattern {
			httpErr(w, http.StatusBadRequest, fmt.Sprintf("grep pattern longer than %d bytes", sliceMaxPattern))
			return
		}
		if re, err = regexp.Compile(pat); err != nil {
			httpErr(w, http.StatusBadRequest, "grep: "+err.Error())
			return
		}
	}
	ctxLines, err := sliceInt(q.Get("context"), 1, 0, sliceMaxContext)
	if err != nil {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("context must be between 0 and %d", sliceMaxContext))
		return
	}
	maxMatches, err := sliceInt(q.Get("maxMatches"), sliceDefaultMatches, 1, sliceMaxMatches)
	if err != nil {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("maxMatches must be between 1 and %d", sliceMaxMatches))
		return
	}

	headers, hash, stored := f.ReqHeaders, f.ReqBodyHash, f.ReqLen
	mime := headerContentType(f.ReqHeaders)
	if side == "res" {
		headers, hash, stored, mime = f.ResHeaders, f.ResBodyHash, f.ResLen, f.Mime
	}
	out := flowSliceJSON{ID: f.ID, Side: side, Mime: mime, BodyHash: hash, Offset: offset}
	enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding")))
	identity := enc == "" || enc == "identity"

	if re == nil && identity {
		// Plain body: stream only the sniff window and the requested range.
		if err := h.sliceStream(&out, hash, stored, offset, limit); err != nil {
			httpFileNotFoundOrInternal(w, err, "flow body not found")
			return
		}
		out.Head = flowHead(f, side, headers)
		writeJSON(w, http.StatusOK, out)
		return
	}
	dispHeaders, body, capped, err := h.bodyForDisplayLimit(hash, headers, decodeMax)
	if err != nil {
		httpFileNotFoundOrInternal(w, err, "flow body not found")
		return
	}
	out.Head = flowHead(f, side, dispHeaders)
	out.Decoded = !identity && firstHeader(dispHeaders, "Content-Encoding") == ""
	out.Capped = capped
	out.BodyBytes = int64(len(body))
	out.Binary = looksBinary(body)
	if out.Binary {
		out.HexHead = hex.Dump(body[:min(len(body), sliceHexHeadBytes)])
	}
	if re != nil {
		out.Grep = grepBody(body, re, ctxLines, maxMatches)
		writeJSON(w, http.StatusOK, out)
		return
	}
	if !out.Binary {
		fillTextWindow(&out, body, offset, limit, int64(len(body)))
	}
	writeJSON(w, http.StatusOK, out)
}

// sliceStream fills out from an unencoded stored body without loading it.
func (h *Hub) sliceStream(out *flowSliceJSON, hash string, stored int64, offset, limit int) error {
	out.BodyBytes = stored
	if hash == "" {
		return nil
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return err
	}
	defer func() { rc.Close() }()
	sniff, err := io.ReadAll(io.LimitReader(rc, sliceSniffBytes))
	if err != nil {
		return err
	}
	out.Binary = looksBinary(sniff)
	if out.Binary {
		out.HexHead = hex.Dump(sniff[:min(len(sniff), sliceHexHeadBytes)])
		return nil
	}
	if int64(offset) >= stored {
		return nil
	}
	// Read a few bytes either side so the window can snap to rune boundaries.
	lead := min(offset, utf8.UTFMax)
	// A stored body is a plain file: seek to the window. Reading and discarding
	// everything before it made page k cost O(offset), so scrolling a large
	// body was quadratic. The stored bytes are the displayed bytes here (this
	// path is only taken for a body without a Content-Encoding), so a file
	// offset is a text offset. A reader that cannot seek is skipped forward.
	if sk, ok := rc.(io.Seeker); ok {
		if _, err := sk.Seek(int64(offset-lead), io.SeekStart); err != nil {
			return err
		}
	} else {
		rc.Close()
		if rc, err = h.st.OpenBody(hash); err != nil {
			rc = io.NopCloser(strings.NewReader(""))
			return err
		}
		if _, err := io.CopyN(io.Discard, rc, int64(offset-lead)); err != nil && err != io.EOF {
			return err
		}
	}
	win, err := io.ReadAll(io.LimitReader(rc, int64(lead+limit+utf8.UTFMax)))
	if err != nil {
		return err
	}
	fillTextWindowAt(out, win, lead, offset, limit, stored)
	return nil
}

// fillTextWindow sets the text window of a fully loaded body.
func fillTextWindow(out *flowSliceJSON, body []byte, offset, limit int, total int64) {
	if offset >= len(body) {
		return
	}
	lead := min(offset, utf8.UTFMax)
	end := min(len(body), offset+limit+utf8.UTFMax)
	fillTextWindowAt(out, body[offset-lead:end], lead, offset, limit, total)
}

// fillTextWindowAt cuts [offset, offset+limit) out of win (which starts lead
// bytes before offset), moving both edges to UTF-8 rune boundaries.
func fillTextWindowAt(out *flowSliceJSON, win []byte, lead, offset, limit int, total int64) {
	start := lead
	for start > 0 && start < len(win) && !utf8.RuneStart(win[start]) {
		start-- // snap back so the first rune is whole
	}
	end := min(len(win), lead+limit)
	for end > start && end < len(win) && !utf8.RuneStart(win[end]) {
		end--
	}
	out.Offset = offset - (lead - start)
	out.Text = string(win[start:end])
	out.Shown = end - start
	next := int64(out.Offset + out.Shown)
	if next < total {
		out.More, out.Next = true, int(next)
	}
}

// looksBinary reports whether a body sample is not readable text: it holds a
// NUL byte or is not valid UTF-8 (ignoring a rune cut at the sample's end).
func looksBinary(b []byte) bool {
	sample := b[:min(len(b), sliceSniffBytes)]
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	for i := 0; i < utf8.UTFMax && len(sample) > 0 && !utf8.Valid(sample); i++ {
		sample = sample[:len(sample)-1]
	}
	return !utf8.Valid(sample)
}

// grepBody returns the lines matching re with ctx lines of context each.
func grepBody(body []byte, re *regexp.Regexp, ctx, maxMatches int) *sliceGrep {
	g := &sliceGrep{Pattern: re.String(), Matches: []sliceMatch{}}
	lines := bytes.Split(body, []byte{'\n'})
	offsets := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		offsets[i] = pos
		pos += len(l) + 1
	}
	for i, l := range lines {
		loc := re.FindIndex(l)
		if loc == nil {
			continue
		}
		g.Count++
		if len(g.Matches) >= maxMatches {
			g.Truncated = true
			continue
		}
		var sb strings.Builder
		for j := max(0, i-ctx); j <= min(len(lines)-1, i+ctx); j++ {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(displayLine(lines[j]))
		}
		g.Matches = append(g.Matches, sliceMatch{Line: i + 1, Offset: offsets[i] + loc[0], Text: sb.String()})
	}
	return g
}

// displayLine renders one body line as safe, bounded text: invalid UTF-8 and
// control characters become '.', and long lines are cut.
func displayLine(l []byte) string {
	s := strings.TrimRight(strings.ToValidUTF8(string(l), "."), "\r")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' || r == 0x7f {
			return '.'
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > sliceMaxLineRunes {
		s = string([]rune(s)[:sliceMaxLineRunes]) + "…"
	}
	return s
}

// flowHead renders the start line and headers of one side of a flow, in the
// same form GET /api/flows/{id}/raw uses.
func flowHead(f *store.Flow, side string, headers map[string][]string) string {
	var b bytes.Buffer
	if side == "res" {
		fmt.Fprintf(&b, "%s %d %s\r\n", orVal(f.HTTPVersion, "HTTP/1.1"), f.Status, http.StatusText(f.Status))
		writeHeaders(&b, headers, "")
	} else {
		fmt.Fprintf(&b, "%s %s %s\r\n", f.Method, orVal(f.Path, "/"), orVal(f.HTTPVersion, "HTTP/1.1"))
		writeHeaders(&b, headers, f.Host)
	}
	return b.String()
}

func sliceInt(raw string, def, lo, hi int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("out of range")
	}
	return n, nil
}
