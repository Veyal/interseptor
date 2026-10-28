package preview

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// The line planner decides which rows a pane shows before anything is drawn,
// so window merging, gap markers and the row budget are tested without pixels.

type rowKind uint8

const (
	rowLine   rowKind = iota // first row of a logical line (numbered)
	rowCont                  // soft-wrap continuation (no number)
	rowMarker                // "… N lines hidden …" and similar notes
)

type planRow struct {
	kind    rowKind
	lineNo  int    // 1-based logical line for rowLine and rowCont
	text    string // row text (already wrapped)
	hl      []span // highlighted byte spans within text
	inRange bool   // row belongs to a requested line range
	off     int    // byte offset of text within its logical line
}

func (r planRow) match() bool { return len(r.hl) > 0 }

func markerRow(format string, a ...any) planRow {
	return planRow{kind: rowMarker, text: "… " + fmt.Sprintf(format, a...) + " …"}
}

// minPlanRows keeps room for the first line plus the end markers.
const minPlanRows = 4

// logicalLines splits a displayed HTTP message into lines (CRLF normalized).
func logicalLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	// A final newline ends the last line; it does not start an empty one.
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// headerEnd is the index of the blank line ending the header block, or n.
func headerEnd(lines []string) int {
	for i, l := range lines {
		if i > 0 && l == "" {
			return i
		}
	}
	return len(lines)
}

// wrapSegments soft-wraps a line every maxCols runes, as byte ranges.
func wrapSegments(line string, maxCols int) []span {
	if maxCols <= 0 || utf8.RuneCountInString(line) <= maxCols {
		return []span{{0, len(line)}}
	}
	var out []span
	start, n := 0, 0
	for i := range line {
		if n == maxCols {
			out = append(out, span{start, i})
			start, n = i, 0
		}
		n++
	}
	return append(out, span{start, len(line)})
}

// clipSpans returns hits intersecting [seg.start, seg.end) relative to seg.
func clipSpans(hits []span, seg span) []span {
	var out []span
	for _, h := range hits {
		s, e := max(h.start, seg.start), min(h.end, seg.end)
		if s < e {
			out = append(out, span{s - seg.start, e - seg.start})
		}
	}
	return out
}

// lineRows wraps logical line i into rows carrying its highlights and range flag.
func lineRows(lines []string, i int, f paneFocus, maxCols int) []planRow {
	line := lines[i]
	var hits []span
	if i < len(f.hits) {
		hits = f.hits[i]
	}
	inRange := false
	for _, r := range f.ranges {
		if i >= r[0] && i <= r[1] {
			inRange = true
			break
		}
	}
	segs := wrapSegments(line, maxCols)
	rows := make([]planRow, 0, len(segs))
	for k, seg := range segs {
		kind := rowLine
		if k > 0 {
			kind = rowCont
		}
		rows = append(rows, planRow{kind: kind, lineNo: i + 1, text: line[seg.start:seg.end], hl: clipSpans(hits, seg), inRange: inRange, off: seg.start})
	}
	return rows
}

// planTop keeps the first maxRows rows and ends with an explicit count marker.
// When the cut falls inside a soft-wrapped line the marker also counts the
// characters of that line left out, so a single huge line never reads as
// "0 more lines".
func planTop(lines []string, f paneFocus, maxRows, maxCols int) []planRow {
	if len(lines) == 0 {
		return []planRow{{kind: rowMarker, text: "(empty)"}}
	}
	maxRows = max(maxRows, 2)
	var rows []planRow
	for i := range lines {
		rows = append(rows, lineRows(lines, i, f, maxCols)...)
		if len(rows) > maxRows {
			break
		}
	}
	if len(rows) <= maxRows {
		return rows
	}
	cut := rows[maxRows-1:]
	rows = rows[:maxRows-1]
	shown := rows[len(rows)-1].lineNo
	more := len(lines) - shown
	hidden := 0
	for _, r := range cut {
		if r.lineNo == shown && r.kind == rowCont {
			hidden += utf8.RuneCountInString(r.text)
		}
	}
	switch {
	case hidden > 0 && more > 0:
		return append(rows, markerRow("%d chars of line %d and %s hidden (showing 1–%d of %d)", hidden, shown, countNoun(more, "more line"), shown, len(lines)))
	case hidden > 0:
		return append(rows, markerRow("%d more chars of line %d hidden (showing 1–%d of %d)", hidden, shown, shown, len(lines)))
	}
	noun := "lines"
	if more == 1 {
		noun = "line"
	}
	return append(rows, markerRow("%d more %s (showing 1–%d of %d)", more, noun, shown, len(lines)))
}

// Context kept around the center segment of a clipped line.
const (
	clipLead = 2 // segments before the center
	clipTail = 3 // segments after the center
)

// clipLine fits one logical line's rows into room rows (room >= 2), keeping
// the segment at center plus a few segments of context and replacing the
// rest with "… N chars of line L hidden …" markers.
func clipLine(rows []planRow, center, room int) []planRow {
	n := len(rows)
	if n <= room {
		return rows
	}
	lineNo := rows[0].lineNo
	chars := func(rs []planRow) int {
		c := 0
		for _, r := range rs {
			c += utf8.RuneCountInString(r.text)
		}
		return c
	}
	center = min(max(center, 0), n-1)
	a, b := center-min(clipLead, center), min(n, center+1+clipTail)
	need := func() int {
		m := b - a
		if a > 0 {
			m++
		}
		if b < n {
			m++
		}
		return m
	}
	for need() > room {
		switch {
		case b > center+1:
			b--
		case a < center:
			a++
		default:
			// Only the center fits beside one marker: one combined note.
			return []planRow{markerRow("%d of %d chars of line %d hidden", chars(rows)-chars(rows[center:center+1]), chars(rows), lineNo), rows[center]}
		}
	}
	var out []planRow
	if a > 0 {
		out = append(out, markerRow("%d chars of line %d hidden", chars(rows[:a]), lineNo))
	}
	out = append(out, rows[a:b]...)
	if b < n {
		out = append(out, markerRow("%d more chars of line %d hidden", chars(rows[b:]), lineNo))
	}
	return out
}

// firstMatchRow is the index of the first highlighted row, or 0.
func firstMatchRow(rows []planRow) int {
	for i, r := range rows {
		if r.match() {
			return i
		}
	}
	return 0
}

type window struct{ lo, hi int }

// focusWindows turns matches and ranges into merged context windows. When
// headers are collapsed the windows skip the header block (line 0 stays).
func focusWindows(lines []string, f paneFocus, ctx int) (wins []window, collapse bool, hEnd int) {
	n := len(lines)
	hEnd = headerEnd(lines)
	var anchors []window
	for i, h := range f.hits {
		if len(h) > 0 {
			anchors = append(anchors, window{i, i})
		}
	}
	for _, r := range f.ranges {
		anchors = append(anchors, window{r[0], r[1]})
	}
	inHeaders := func(w window) bool { return w.lo <= hEnd-1 && w.hi >= 1 }
	collapse = hEnd > 1
	for _, a := range anchors {
		if inHeaders(a) {
			collapse = false
		}
	}
	wins = []window{{0, 0}}
	for _, a := range anchors {
		w := window{max(0, a.lo-ctx), min(n-1, a.hi+ctx)}
		if collapse && w.lo <= hEnd {
			if a.hi == 0 {
				continue // a first-line match is already shown
			}
			// Context never reaches into collapsed headers, but an anchor on
			// the blank header/body separator itself is kept and drawn.
			w.lo = min(a.lo, hEnd+1)
		}
		if w.lo <= w.hi {
			wins = append(wins, w)
		}
	}
	return mergeWindows(wins, collapse, hEnd), collapse, hEnd
}

// mergeWindows joins overlapping windows and swallows one-line gaps (a marker
// would cost the same row), except across a collapsed header block.
func mergeWindows(wins []window, collapse bool, hEnd int) []window {
	sort.Slice(wins, func(a, b int) bool { return wins[a].lo < wins[b].lo })
	out := []window{wins[0]}
	for _, w := range wins[1:] {
		last := &out[len(out)-1]
		gapIsHeaders := collapse && last.hi < hEnd && w.lo > hEnd
		if w.lo <= last.hi+2 && !gapIsHeaders {
			last.hi = max(last.hi, w.hi)
			continue
		}
		out = append(out, w)
	}
	return out
}

// gapRows describes hidden lines lo..hi (0-based) as one or two marker rows.
func gapRows(lo, hi int, collapse bool, hEnd int) []planRow {
	if lo > hi {
		return nil
	}
	var rows []planRow
	if collapse && lo <= hEnd {
		if n := hEnd - 1; n > 0 {
			rows = append(rows, markerRow("%s hidden", countNoun(n, "header")))
		}
		lo = hEnd + 1
	}
	if lo > hi {
		return rows
	}
	if lo == hi {
		return append(rows, markerRow("1 line hidden (%d)", lo+1))
	}
	return append(rows, markerRow("%s hidden (%d–%d)", countNoun(hi-lo+1, "line"), lo+1, hi+1))
}

// planFocus keeps the first line and ±ctx lines around every match and range
// inside a maxRows budget, earliest first. With no anchors it is the compact
// form: first line, collapsed headers, one gap marker. A soft-wrapped line
// too long for the budget is clipped around its first match with "… N chars
// hidden …" markers, so a match inside a huge minified line is still drawn.
func planFocus(lines []string, f paneFocus, ctx, maxRows, maxCols int) []planRow {
	if len(lines) == 0 {
		return []planRow{{kind: rowMarker, text: "(empty)"}}
	}
	maxRows = max(maxRows, minPlanRows)
	const reserve = 2 // trailing gap marker + "more matches" marker
	wins, collapse, hEnd := focusWindows(lines, f, ctx)
	var rows []planRow
	shownTo := -1 // last logical line (partly) shown
	for _, w := range wins {
		gap := gapRows(shownTo+1, w.lo-1, collapse, hEnd)
		room := maxRows - reserve - len(rows) - len(gap)
		body, last, done := windowRows(lines, f, w, maxCols, room)
		if len(body) > 0 {
			rows = append(rows, gap...)
			rows = append(rows, body...)
			shownTo = last
		}
		if !done {
			break
		}
	}
	rows = append(rows, gapRows(shownTo+1, len(lines)-1, collapse, hEnd)...)
	if k := undrawnHits(rows, f); k > 0 {
		rows = append(rows, markerRow("%s not shown", countNoun(k, "more match")))
	}
	return rows
}

// windowRows plans lines w.lo..w.hi into at most room rows. It returns the
// rows, the last logical line (partly) included and whether the window fit.
// When it does not fit, context lines shrink to one segment and an anchor
// line is clipped around its first match.
func windowRows(lines []string, f paneFocus, w window, maxCols, room int) ([]planRow, int, bool) {
	var all []planRow
	for i := w.lo; i <= w.hi; i++ {
		all = append(all, lineRows(lines, i, f, maxCols)...)
	}
	if len(all) <= room {
		return all, w.hi, true
	}
	var out []planRow
	last := -1
	for i := w.lo; i <= w.hi; i++ {
		lr := lineRows(lines, i, f, maxCols)
		anchor := lineAnchored(f, i)
		if !anchor && len(lr) > 2 {
			lr = clipLine(lr, 0, 2)
		}
		left := room - len(out)
		if len(lr) > left {
			if left >= 2 {
				out = append(out, clipLine(lr, firstMatchRow(lr), left)...)
				last = i
			}
			return out, last, false
		}
		out = append(out, lr...)
		last = i
	}
	return out, last, true
}

// lineAnchored reports whether logical line i has a match or is in a range.
func lineAnchored(f paneFocus, i int) bool {
	if i < len(f.hits) && len(f.hits[i]) > 0 {
		return true
	}
	for _, r := range f.ranges {
		if i >= r[0] && i <= r[1] {
			return true
		}
	}
	return false
}

// undrawnHits counts highlighted occurrences with no drawn row, including
// those on a line that is only partly shown.
func undrawnHits(rows []planRow, f paneFocus) int {
	drawn := map[int][]span{} // lineNo-1 → drawn byte ranges
	for _, r := range rows {
		if r.kind != rowMarker {
			drawn[r.lineNo-1] = append(drawn[r.lineNo-1], span{r.off, r.off + len(r.text)})
		}
	}
	n := 0
	for i, hs := range f.hits {
		for _, h := range hs {
			seen := false
			for _, d := range drawn[i] {
				if h.start < d.end && h.end > d.start {
					seen = true
					break
				}
			}
			if !seen {
				n++
			}
		}
	}
	return n
}

// drawnMatches reports whether any planned row carries a highlight.
func drawnMatches(rows []planRow) bool {
	for _, r := range rows {
		if r.match() {
			return true
		}
	}
	return false
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ch") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
