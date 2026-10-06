package preview

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

// Flow-vs-flow diff render limits.
const (
	flowDiffMaxLines   = 120 // diff lines drawn before "+N more lines"
	flowDiffMaxCols    = 110 // columns per diff line (tabs expanded, ellipsis)
	flowDiffMaxHeaders = 40  // header delta rows drawn before "+N more headers"
	flowDiffTabWidth   = 4
	flowDiffLineH      = 18
	flowDiffRowH       = 24
)

// FlowSide is one side of a flow comparison.
type FlowSide struct {
	FlowID int64
	Method string
	URL    string
	Status int
	Length int
	TimeMs int
}

// FlowHeaderDelta is one header that differs. Kind is added, removed or
// changed (add, remove and change are accepted as aliases).
type FlowHeaderDelta struct {
	Name string
	Kind string
	A    string
	B    string
}

// FlowBodyDelta is one body diff line. Kind is added, removed or context
// (add/+, remove/-, "" and " " are accepted as aliases).
type FlowBodyDelta struct {
	Kind string
	Line string
}

// FlowDiffInput is plain data for RenderFlowDiff. The adapter is responsible
// for redacting secrets before building it.
type FlowDiffInput struct {
	A, B         FlowSide
	HeaderDeltas []FlowHeaderDelta
	BodyDeltas   []FlowBodyDelta
	Summary      string
}

// diffKind is the normalised delta kind.
type diffKind int

const (
	diffContext diffKind = iota
	diffAdded
	diffRemoved
	diffChanged
)

func normDiffKind(k string) diffKind {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "add", "added", "+":
		return diffAdded
	case "remove", "removed", "-":
		return diffRemoved
	case "change", "changed", "~":
		return diffChanged
	}
	return diffContext
}

func (k diffKind) glyph() string {
	switch k {
	case diffAdded:
		return "+"
	case diffRemoved:
		return "-"
	case diffChanged:
		return "~"
	}
	return " "
}

// expandTabs replaces tabs with spaces to the next tab stop and drops other
// control characters so mono column maths stays exact.
func expandTabs(s string) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := flowDiffTabWidth - col%flowDiffTabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r == '\r' || r == '\n':
		case r < 0x20 || r == 0x7f:
			b.WriteRune('?')
			col++
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// clipCols limits s to max runes, ending in "..." when shortened.
func clipCols(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

// flowDiffLine is one prepared diff line: glyph prefix plus clipped text.
type flowDiffLine struct {
	kind diffKind
	text string // prefix glyph + space + clipped content
}

// prepareDiffLines expands tabs, truncates to flowDiffMaxCols and caps the
// list at limit lines, returning how many lines were dropped.
func prepareDiffLines(in []FlowBodyDelta, limit int) ([]flowDiffLine, int) {
	if limit > flowDiffMaxLines {
		limit = flowDiffMaxLines
	}
	if limit < 0 {
		limit = 0
	}
	n := len(in)
	more := 0
	if n > limit {
		more = n - limit
		n = limit
	}
	out := make([]flowDiffLine, 0, n)
	for _, d := range in[:n] {
		k := normDiffKind(d.Kind)
		out = append(out, flowDiffLine{kind: k, text: clipCols(k.glyph()+" "+expandTabs(d.Line), flowDiffMaxCols)})
	}
	return out, more
}

// flowDiffIdentical reports whether nothing differs between the two flows.
func flowDiffIdentical(in FlowDiffInput) bool {
	if in.A.Status != in.B.Status || in.A.Length != in.B.Length || len(in.HeaderDeltas) > 0 {
		return false
	}
	for _, d := range in.BodyDeltas {
		if normDiffKind(d.Kind) != diffContext {
			return false
		}
	}
	return true
}

func countBodyChanges(in []FlowBodyDelta) (add, del int) {
	for _, d := range in {
		switch normDiffKind(d.Kind) {
		case diffAdded:
			add++
		case diffRemoved:
			del++
		}
	}
	return
}

func flowDiffSummary(in FlowDiffInput) string {
	head := fmt.Sprintf("Flow #%d vs #%d", in.A.FlowID, in.B.FlowID)
	if flowDiffIdentical(in) {
		return head + ": no differences"
	}
	add, del := countBodyChanges(in.BodyDeltas)
	return fmt.Sprintf("%s: status %d -> %d, length %d -> %d, %d header deltas, +%d/-%d body lines",
		head, in.A.Status, in.B.Status, in.A.Length, in.B.Length, len(in.HeaderDeltas), add, del)
}

func flowDiffAlt(in FlowDiffInput) string {
	parts := []string{
		fmt.Sprintf("Flow diff of %s %s (flow %d) against %s %s (flow %d)",
			in.A.Method, in.A.URL, in.A.FlowID, in.B.Method, in.B.URL, in.B.FlowID),
	}
	if flowDiffIdentical(in) {
		parts = append(parts, fmt.Sprintf("No differences: both returned status %d with length %d", in.A.Status, in.A.Length))
		return AltFromParts(parts...)
	}
	parts = append(parts, fmt.Sprintf("Status %d versus %d, length %d versus %d bytes, time %d versus %d ms",
		in.A.Status, in.B.Status, in.A.Length, in.B.Length, in.A.TimeMs, in.B.TimeMs))
	var added, removed, changed []string
	for _, h := range in.HeaderDeltas {
		switch normDiffKind(h.Kind) {
		case diffAdded:
			added = append(added, h.Name)
		case diffRemoved:
			removed = append(removed, h.Name)
		default:
			changed = append(changed, h.Name)
		}
	}
	if len(in.HeaderDeltas) > 0 {
		parts = append(parts, fmt.Sprintf("Headers: %d added, %d removed, %d changed", len(added), len(removed), len(changed)))
	}
	add, del := countBodyChanges(in.BodyDeltas)
	if add+del > 0 {
		parts = append(parts, fmt.Sprintf("Body: %d lines added, %d lines removed", add, del))
	}
	return AltFromParts(parts...)
}

// RenderFlowDiff draws a deterministic flow-vs-flow diff: two header cards,
// a header delta table and a unified body diff with +/- glyphs (never colour
// only), capped at 120 diff lines.
func RenderFlowDiff(in FlowDiffInput, o Opts) (Rendered, error) {
	identical := flowDiffIdentical(in)
	hdrs := in.HeaderDeltas
	hdrMore := 0
	if len(hdrs) > flowDiffMaxHeaders {
		hdrMore = len(hdrs) - flowDiffMaxHeaders
		hdrs = hdrs[:flowDiffMaxHeaders]
	}
	summary := flowDiffSummary(in)
	prov := fmt.Sprintf("Flows #%d and #%d. Redacted by the adapter. Only recorded status, length, time, headers and body are compared.", in.A.FlowID, in.B.FlowID)

	f := frame{
		Title:      "Flow-vs-flow diff",
		Provenance: prov,
		BodyHeight: func(rows int) int {
			gotLines, gotMore := planDiffLines(in.BodyDeltas, rows, len(hdrs), hdrMore, identical, in.Summary != "")
			return flowDiffBodyHeight(len(hdrs), hdrMore, len(gotLines), gotMore, identical, in.Summary != "")
		},
		Draw: func(c *canvas, body image.Rectangle, rows int) {
			gotLines, gotMore := planDiffLines(in.BodyDeltas, rows, len(hdrs), hdrMore, identical, in.Summary != "")
			drawFlowDiff(c, body, in, hdrs, hdrMore, gotLines, gotMore, identical)
		},
	}
	png, w, h, err := renderFrame(o, f)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{PNG: png, Alt: flowDiffAlt(in), Summary: summary, Kind: KindFlowDiff, Width: w, Height: h}, nil
}

// planDiffLines picks the diff lines to draw: at most flowDiffMaxLines (120)
// and the row budget, further reduced so the image stays within the maximum
// render height. The remainder is reported as "+N more lines".
func planDiffLines(in []FlowBodyDelta, rows, nHdr, hdrMore int, identical, hasNote bool) ([]flowDiffLine, int) {
	fixed := titleH + footerH + flowDiffBodyHeight(nHdr, hdrMore, 1, 1, identical, hasNote) - flowDiffLineH
	fit := (maxRenderHeight - fixed) / flowDiffLineH
	return prepareDiffLines(in, minInt(rows, fit))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const (
	fdCardH   = 84
	fdPad     = 16
	fdBannerH = 34
)

func flowDiffBodyHeight(nHdr, hdrMore, nLines, more int, identical, hasNote bool) int {
	h := fdPad + fdCardH + 10 + fdBannerH
	if hasNote {
		h += 22
	}
	if identical {
		return h + fdPad
	}
	if nHdr > 0 {
		h += 26 + flowDiffRowH + nHdr*flowDiffRowH
		if hdrMore > 0 {
			h += 22
		}
		h += 14
	}
	h += 26
	if nLines == 0 {
		h += 24
	} else {
		h += 8 + nLines*flowDiffLineH + 8
	}
	if more > 0 {
		h += 26
	}
	return h + fdPad
}

// blend mixes fg over bg at alpha a/255.
func blend(fg, bg color.RGBA, a int) color.RGBA {
	m := func(f, b uint8) uint8 { return uint8((int(f)*a + int(b)*(255-a)) / 255) }
	return color.RGBA{m(fg.R, bg.R), m(fg.G, bg.G), m(fg.B, bg.B), 0xff}
}

func (c *canvas) kindColor(k diffKind) color.RGBA {
	switch k {
	case diffAdded:
		return c.pal.success
	case diffRemoved:
		return c.pal.blocked
	case diffChanged:
		return c.pal.client
	}
	return c.pal.muted
}

func drawFlowDiff(c *canvas, body image.Rectangle, in FlowDiffInput, hdrs []FlowHeaderDelta, hdrMore int, lines []flowDiffLine, more int, identical bool) {
	x0, x1 := body.Min.X+frameGut, body.Max.X-frameGut
	y := body.Min.Y + fdPad
	half := (x1 - x0 - 12) / 2
	drawFlowCard(c, x0, y, half, "A", in.A)
	drawFlowCard(c, x0+half+12, y, half, "B", in.B)
	y += fdCardH + 10

	// banner: delta line or "no differences"
	if identical {
		c.rect(x0, y, x1-x0, fdBannerH-6, blend(c.pal.success, c.pal.paper, 28))
		c.strokeRect(x0, y, x1-x0, fdBannerH-6, c.pal.success)
		c.text(x0+10, y+5, "= No differences: same status, length, headers and body", fontBold, 14, c.pal.ink)
	} else {
		c.rect(x0, y, x1-x0, fdBannerH-6, c.pal.panel)
		c.strokeRect(x0, y, x1-x0, fdBannerH-6, c.pal.grid)
		c.text(x0+10, y+5, c.truncate(fontBold, 13, flowDeltaLine(in), x1-x0-20), fontBold, 13, c.pal.ink)
	}
	y += fdBannerH
	if in.Summary != "" {
		c.text(x0, y-2, c.truncate(fontSans, 12, in.Summary, x1-x0), fontSans, 12, c.pal.muted)
		y += 22
	}
	if identical {
		return
	}

	if len(hdrs) > 0 {
		c.text(x0, y, fmt.Sprintf("Header deltas (%d)", len(in.HeaderDeltas)), fontBold, 14, c.pal.ink)
		y += 26
		y = drawHeaderTable(c, x0, x1, y, hdrs, hdrMore)
		y += 14
	}

	c.text(x0, y, "Body diff", fontBold, 14, c.pal.ink)
	add, del := countBodyChanges(in.BodyDeltas)
	c.text(x0+c.measure(fontBold, 14, "Body diff")+10, y+2, fmt.Sprintf("+%d / -%d lines", add, del), fontSans, 12, c.pal.muted)
	y += 26
	if len(lines) == 0 {
		c.text(x0, y, "No body differences recorded.", fontSans, 13, c.pal.muted)
		return
	}
	boxH := 8 + len(lines)*flowDiffLineH + 8
	c.rect(x0, y, x1-x0, boxH, c.pal.panel)
	c.strokeRect(x0, y, x1-x0, boxH, c.pal.grid)
	ly := y + 8
	for _, l := range lines {
		switch l.kind {
		case diffAdded, diffRemoved:
			c.rect(x0+1, ly, x1-x0-2, flowDiffLineH, blend(c.kindColor(l.kind), c.pal.paper, 30))
			c.rect(x0+1, ly, 3, flowDiffLineH, c.kindColor(l.kind))
		}
		col := c.pal.ink
		if l.kind == diffContext {
			col = c.pal.muted
		}
		c.text(x0+10, ly+2, l.text, fontMono, 12, col)
		ly += flowDiffLineH
	}
	y += boxH + 6
	if more > 0 {
		c.text(x0, y, fmt.Sprintf("+%d more lines", more), fontBold, 13, c.pal.muted)
	}
}

func flowDeltaLine(in FlowDiffInput) string {
	var parts []string
	if in.A.Status != in.B.Status {
		parts = append(parts, fmt.Sprintf("status %d -> %d", in.A.Status, in.B.Status))
	} else {
		parts = append(parts, fmt.Sprintf("status %d same", in.A.Status))
	}
	parts = append(parts, fmt.Sprintf("length %+d B", in.B.Length-in.A.Length))
	parts = append(parts, fmt.Sprintf("time %+d ms", in.B.TimeMs-in.A.TimeMs))
	return "B vs A: " + strings.Join(parts, "   |   ")
}

func drawFlowCard(c *canvas, x, y, w int, tag string, s FlowSide) {
	c.rect(x, y, w, fdCardH, c.pal.panel)
	c.strokeRect(x, y, w, fdCardH, c.pal.grid)
	c.rect(x, y, 4, fdCardH, c.pal.accent)
	cx := x + 14
	cx += c.chip(cx, y+8, tag, c.pal.ink, c.pal.paper) + 8
	c.text(cx, y+10, fmt.Sprintf("Flow #%d", s.FlowID), fontBold, 13, c.pal.ink)
	method := strings.TrimSpace(s.Method)
	mw := c.measure(fontBold, 13, method)
	c.text(x+14, y+34, method, fontBold, 13, c.pal.ink)
	c.text(x+14+mw+8, y+34, c.truncate(fontSans, 13, s.URL, w-30-mw-8), fontSans, 13, c.pal.muted)
	sc := c.pal.statusColor(s.Status)
	label := fmt.Sprintf("%s %d", statusGlyph(s.Status), s.Status)
	cw := c.chip(x+14, y+56, label, sc, c.pal.paper)
	c.text(x+14+cw+12, y+58, fmt.Sprintf("%d bytes   %d ms", s.Length, s.TimeMs), fontSans, 13, c.pal.ink)
}

func drawHeaderTable(c *canvas, x0, x1, y int, hdrs []FlowHeaderDelta, more int) int {
	gl, nameW := 36, 200
	valW := (x1 - x0 - gl - nameW) / 2
	colW := []int{gl, nameW, valW, x1 - x0 - gl - nameW - valW}
	c.rect(x0, y, x1-x0, flowDiffRowH, c.pal.panel)
	heads := []string{"", "Header", "A", "B"}
	cx := x0
	for i, h := range heads {
		c.text(cx+8, y+5, h, fontBold, 12, c.pal.muted)
		cx += colW[i]
	}
	rows := len(hdrs) + 1
	c.tableGrid(x0, y, colW, flowDiffRowH, rows)
	for i, h := range hdrs {
		ry := y + (i+1)*flowDiffRowH
		k := normDiffKind(h.Kind)
		kc := c.kindColor(k)
		c.rect(x0+1, ry+1, colW[0]-1, flowDiffRowH-1, blend(kc, c.pal.paper, 30))
		c.text(x0+13, ry+4, k.glyph(), fontMono, 14, kc)
		cx := x0 + colW[0]
		c.text(cx+8, ry+4, c.truncate(fontBold, 12, h.Name, colW[1]-16), fontBold, 12, c.pal.ink)
		cx += colW[1]
		a, b := h.A, h.B
		if k == diffAdded {
			a = "(absent)"
		}
		if k == diffRemoved {
			b = "(absent)"
		}
		c.text(cx+8, ry+5, c.truncate(fontMono, 12, expandTabs(a), colW[2]-16), fontMono, 12, c.pal.ink)
		cx += colW[2]
		c.text(cx+8, ry+5, c.truncate(fontMono, 12, expandTabs(b), colW[3]-16), fontMono, 12, c.pal.ink)
	}
	y += rows * flowDiffRowH
	if more > 0 {
		c.text(x0, y+4, fmt.Sprintf("+%d more headers", more), fontBold, 13, c.pal.muted)
		y += 22
	}
	return y
}
