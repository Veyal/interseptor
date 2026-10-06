package preview

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"unicode"
)

// StripRow is one payload attempt. Rows are drawn in Seq (dispatch) order.
type StripRow struct {
	Seq     int
	Payload string
	Status  int
	Length  int
	TimeMs  int
	Matched bool
	Anomaly bool
}

// StripInput feeds RenderIntruderStrip. Mask forces payload masking; payloads
// that look like credentials are always masked.
type StripInput struct {
	RunID  string
	Attack string
	Rows   []StripRow
	Mask   bool
}

const (
	stripCell      = 14
	stripGap       = 4
	stripTopPad    = 16
	stripSummaryH  = 48
	stripLegendH   = 50
	stripOutRowH   = 22
	stripMaxOutRow = 12
	stripPayloadW  = 26 // max payload chars shown in outlier list
)

// Shading steps by |length - median| / median: within 5%, within 25%, beyond.
var stripShadeAlpha = [3]float64{0.6, 0.8, 1.0}

type stripOutlier struct {
	Seq     int
	Payload string // unmasked source value; use display() for output
	Status  int
	Length  int
	Delta   int // length - median
	Reasons []string
}

// maskPayload keeps the first two and last character. Length is preserved up
// to 12 runes; longer values are shown as a fixed 12-rune mask.
func maskPayload(s string) string {
	r := []rune(s)
	n := len(r)
	switch {
	case n == 0:
		return ""
	case n <= 3:
		return strings.Repeat("*", n)
	}
	if n > 12 {
		n = 12
	}
	return string(r[:2]) + strings.Repeat("*", n-3) + string(r[len(r)-1:])
}

// looksSecret flags credential-like values: >=20 runes, no whitespace, mixing
// letters and digits (passwords, tokens, API keys).
func looksSecret(s string) bool {
	if len([]rune(s)) < 20 {
		return false
	}
	letter, digit := false, false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			return false
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return letter && digit
}

func (in StripInput) display(p string) string {
	if in.Mask || looksSecret(p) {
		p = maskPayload(p)
	}
	return truncatePayload(p)
}

func stripSorted(in StripInput) []StripRow {
	rows := append([]StripRow(nil), in.Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
	return rows
}

func medianLength(rows []StripRow) int {
	if len(rows) == 0 {
		return 0
	}
	ls := make([]int, len(rows))
	for i, r := range rows {
		ls[i] = r.Length
	}
	sort.Ints(ls)
	m := len(ls) / 2
	if len(ls)%2 == 1 {
		return ls[m]
	}
	return (ls[m-1] + ls[m]) / 2
}

func modalStatus(rows []StripRow) int {
	cnt := map[int]int{}
	for _, r := range rows {
		cnt[r.Status]++
	}
	best, bc := 0, -1
	for s, c := range cnt {
		if c > bc || (c == bc && s < best) {
			best, bc = s, c
		}
	}
	return best
}

// shadeStep returns 0..2 for the length delta against the median.
func shadeStep(length, median int) int {
	d := length - median
	if d < 0 {
		d = -d
	}
	base := median
	if base < 1 {
		base = 1
	}
	switch {
	case d*100 <= base*5:
		return 0
	case d*100 <= base*25:
		return 1
	}
	return 2
}

func stripOutliers(in StripInput) []stripOutlier {
	rows := stripSorted(in)
	med, mode := medianLength(rows), modalStatus(rows)
	var out []stripOutlier
	for _, r := range rows {
		var why []string
		if r.Matched {
			why = append(why, "matched")
		}
		if r.Anomaly {
			why = append(why, "anomaly")
		}
		if r.Status != mode {
			why = append(why, "status")
		}
		if shadeStep(r.Length, med) == 2 {
			why = append(why, "length")
		}
		if len(why) > 0 {
			out = append(out, stripOutlier{r.Seq, r.Payload, r.Status, r.Length, r.Length - med, why})
		}
	}
	return out
}

func stripPlotted(in StripInput, o Opts) int {
	n := len(in.Rows)
	if m := o.maxRows(); n > m {
		return m
	}
	return n
}

func stripPerRow(width int) int {
	n := (width - 2*frameGut + stripGap) / (stripCell + stripGap)
	if n < 1 {
		n = 1
	}
	return n
}

// stripCellRect is the pixel rectangle of cell idx in the full image.
func stripCellRect(width, idx int) image.Rectangle {
	per := stripPerRow(width)
	x := frameGut + (idx%per)*(stripCell+stripGap)
	y := titleH + stripTopPad + stripSummaryH + (idx/per)*(stripCell+stripGap)
	return image.Rect(x, y, x+stripCell, y+stripCell)
}

func blendOver(fg, bg color.RGBA, a float64) color.RGBA {
	mix := func(f, b uint8) uint8 { return uint8(float64(f)*a + float64(b)*(1-a) + 0.5) }
	return color.RGBA{mix(fg.R, bg.R), mix(fg.G, bg.G), mix(fg.B, bg.B), 0xff}
}

func (c *canvas) cellGlyph(rc image.Rectangle, status int, col color.RGBA) {
	x0, y0, x1, y1 := rc.Min.X+3, rc.Min.Y+3, rc.Max.X-4, rc.Max.Y-4
	cx, cy := (rc.Min.X+rc.Max.X)/2, (rc.Min.Y+rc.Max.Y)/2
	switch {
	case status <= 0 || status >= 600: // error: hollow square
		c.strokeRect(x0, y0, x1-x0+1, y1-y0+1, col)
	case isBlockedStatus(status): // X
		c.line(x0, y0, x1, y1, col)
		c.line(x0, y1, x1, y0, col)
		c.line(x0+1, y0, x1, y1-1, col)
		c.line(x0, y1-1, x1-1, y0, col)
	case status >= 500: // centre dot
		c.rect(cx-1, cy-1, 3, 3, col)
	case status >= 400: // diagonal slash
		c.line(x0, y1, x1, y0, col)
	case status >= 200 && status < 300: // tiny check
		c.line(x0, cy, cx-1, y1, col)
		c.line(cx-1, y1, x1, y0, col)
	}
}

func (in StripInput) outlierSet() map[int]bool {
	set := map[int]bool{}
	for _, o := range stripOutliers(in) {
		set[o.Seq] = true
	}
	return set
}

func statusClassCounts(rows []StripRow) string {
	cnt := map[string]int{}
	for _, r := range rows {
		cnt[statusClassLabel(r.Status)]++
	}
	keys := make([]string, 0, len(cnt))
	for k := range cnt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", cnt[k], k))
	}
	return strings.Join(parts, ", ")
}

func distinctOutcomes(rows []StripRow, med int) int {
	seen := map[[2]int]bool{}
	for _, r := range rows {
		seen[[2]int{r.Status, shadeStep(r.Length, med)}] = true
	}
	return len(seen)
}

// RenderIntruderStrip draws one cell per payload in dispatch order, coloured
// by status class and shaded by length delta against the median.
func RenderIntruderStrip(in StripInput, o Opts) (Rendered, error) {
	rows := stripSorted(in)
	plotted := stripPlotted(in, o)
	med, mode := medianLength(rows), modalStatus(rows)
	outs := stripOutliers(in)
	width := o.width()
	per := stripPerRow(width)

	bodyH := func(budget int) int {
		n := len(rows)
		if n > budget {
			n = budget
		}
		gridRows := (n + per - 1) / per
		h := stripTopPad + stripSummaryH + gridRows*(stripCell+stripGap) + 8 + stripLegendH
		if len(rows) > n {
			h += 26
		}
		shown := len(outs)
		if shown > stripMaxOutRow {
			shown = stripMaxOutRow + 1
		}
		return h + 30 + shown*stripOutRowH + 20
	}

	f := frame{
		Title:      "Payload to result heat strip",
		Provenance: stripProvenance(in, len(rows)),
		BodyHeight: bodyH,
		Draw: func(c *canvas, body image.Rectangle, budget int) {
			drawStrip(c, in, rows, outs, med, mode, budget)
		},
	}
	data, w, h, err := renderFrame(o, f)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{
		PNG: data, Width: w, Height: h, Kind: KindIntruderStrip,
		Summary: stripSummary(in, len(rows), plotted, len(outs)),
		Alt:     stripAlt(in, rows, outs, med, plotted),
	}, nil
}

func stripProvenance(in StripInput, n int) string {
	parts := []string{fmt.Sprintf("%d payload results", n)}
	if in.Attack != "" {
		parts = append(parts, "attack "+in.Attack)
	}
	if in.RunID != "" {
		parts = append(parts, "run "+in.RunID)
	}
	return strings.Join(parts, " | ")
}

func stripSummary(in StripInput, n, plotted, outliers int) string {
	s := fmt.Sprintf("%d payloads in dispatch order, %d outliers", n, outliers)
	if n > plotted {
		s += fmt.Sprintf(" (%d plotted, +%d more aggregated)", plotted, n-plotted)
	}
	return s
}

func stripAlt(in StripInput, rows []StripRow, outs []stripOutlier, med, plotted int) string {
	if len(rows) == 0 {
		return "Payload heat strip: no payload results were recorded."
	}
	parts := []string{
		fmt.Sprintf("Payload heat strip of %d payloads in dispatch order with %d distinct outcomes by status and length shade", len(rows), distinctOutcomes(rows, med)),
		fmt.Sprintf("Median response length %d bytes", med),
	}
	if len(rows) > plotted {
		parts = append(parts, fmt.Sprintf("%d cells plotted, +%d more aggregated", plotted, len(rows)-plotted))
	}
	if len(outs) == 0 {
		parts = append(parts, "No outlier payloads")
	} else {
		var l []string
		for i, o := range outs {
			if i == 5 {
				l = append(l, fmt.Sprintf("+%d more", len(outs)-5))
				break
			}
			l = append(l, fmt.Sprintf("%q status %d length %d", in.display(o.Payload), o.Status, o.Length))
		}
		parts = append(parts, fmt.Sprintf("%d outlier payloads: %s", len(outs), strings.Join(l, "; ")))
	}
	return AltFromParts(parts...)
}

func drawStrip(c *canvas, in StripInput, rows []StripRow, outs []stripOutlier, med, mode, budget int) {
	pal := c.pal
	w := c.img.Bounds().Dx()
	y := titleH + stripTopPad
	if len(rows) == 0 {
		c.text(frameGut, y+4, "No payload results were recorded for this run.", fontSans, 14, pal.muted)
		return
	}
	head := fmt.Sprintf("%d payloads in dispatch order", len(rows))
	if in.Attack != "" {
		head += " | attack: " + in.Attack
	}
	c.text(frameGut, y, head, fontBold, 14, pal.ink)
	c.text(frameGut, y+22, fmt.Sprintf("Median length %d bytes | most common status %d | %s", med, mode, statusClassCounts(rows)), fontSans, 12, pal.muted)

	n := len(rows)
	if n > budget {
		n = budget
	}
	outSet := in.outlierSet()
	for i := 0; i < n; i++ {
		r := rows[i]
		rc := stripCellRect(w, i)
		base := pal.statusColor(r.Status)
		fill := blendOver(base, pal.paper, stripShadeAlpha[shadeStep(r.Length, med)])
		c.rect(rc.Min.X, rc.Min.Y, stripCell, stripCell, fill)
		glyphCol := pal.paper
		if contrastRatio(fill, glyphCol) < 3 {
			glyphCol = pal.ink
		}
		c.cellGlyph(rc, r.Status, glyphCol)
		if shadeStep(r.Length, med) == 1 {
			// non-colour length cue: corner tick survives greyscale print
			c.rect(rc.Max.X-4, rc.Max.Y-4, 3, 3, glyphCol)
		}
		if outSet[r.Seq] {
			c.strokeRect(rc.Min.X-1, rc.Min.Y-1, stripCell+2, stripCell+2, pal.ink)
		}
	}
	per := stripPerRow(w)
	gridBottom := titleH + stripTopPad + stripSummaryH + ((n+per-1)/per)*(stripCell+stripGap) + 8
	y = gridBottom
	y += drawStripLegend(c, y, w, mode)
	if len(rows) > n {
		extra := rows[n:]
		c.text(frameGut, y+2, fmt.Sprintf("+%d more payloads not plotted (aggregated: %s)", len(extra), statusClassCounts(extra)), fontBold, 12, pal.ink)
		y += 26
	}
	drawStripOutliers(c, in, outs, y+4, w)
}

// stripLegendSwatches are the three length-shade swatches drawn in the hue of
// the run's common status, matching the cells they explain.
func stripLegendSwatches(pal reportPalette, mode int) [3]color.RGBA {
	var out [3]color.RGBA
	for i := range out {
		out[i] = blendOver(pal.statusColor(mode), pal.paper, stripShadeAlpha[i])
	}
	return out
}

// stripPayloadHeader labels the payload column and says when values are masked.
func stripPayloadHeader(in StripInput, outs []stripOutlier) string {
	for _, o := range outs {
		if in.display(o.Payload) != truncatePayload(o.Payload) {
			return "Payload (masked)"
		}
	}
	return "Payload"
}

func truncatePayload(p string) string {
	r := []rune(p)
	if len(r) > stripPayloadW {
		return string(r[:stripPayloadW-3]) + "..."
	}
	return p
}

func drawStripLegend(c *canvas, y, w, mode int) int {
	pal := c.pal
	h := c.legend(frameGut, y, w-frameGut, []legendItem{
		{"2xx ok (check)", pal.success}, {"3xx", pal.redirect}, {"4xx (slash)", pal.client},
		{"429/403/423 blocked (X)", pal.blocked}, {"5xx (dot)", pal.server}, {"error (square)", pal.errc},
	})
	x := frameGut
	ly := y + h
	c.text(x, ly, "Length vs median:", fontSans, 12, pal.muted)
	x += c.measure(fontSans, 12, "Length vs median:") + 10
	sw := stripLegendSwatches(pal, mode)
	for i, lab := range []string{"within 5%", "5-25% (corner tick)", "over 25% (outlined)"} {
		c.rect(x, ly+2, 12, 12, sw[i])
		if i == 1 {
			c.rect(x+8, ly+10, 3, 3, pal.paper)
		}
		x += 18 + c.text(x+18, ly, lab, fontSans, 12, pal.ink) + 14
	}
	c.strokeRect(x, ly+1, 14, 14, pal.ink)
	c.text(x+20, ly, c.truncate(fontSans, 12, "outlined = outlier (matched, anomaly, differing status or length over 25%)", w-frameGut-x-20), fontSans, 12, pal.ink)
	return stripLegendH
}

func drawStripOutliers(c *canvas, in StripInput, outs []stripOutlier, y, w int) {
	pal := c.pal
	if len(outs) == 0 {
		c.text(frameGut, y+4, "No outliers: every payload matched the common status and length.", fontSans, 13, pal.muted)
		return
	}
	c.text(frameGut, y, fmt.Sprintf("Outlier payloads (%d)", len(outs)), fontBold, 14, pal.ink)
	y += 28
	cols := []int{frameGut, frameGut + 70, frameGut + 380, frameGut + 470, frameGut + 570, frameGut + 680}
	for i, h := range []string{"Request", stripPayloadHeader(in, outs), "Status", "Length", "vs median", "Why"} {
		c.text(cols[i], y, h, fontBold, 12, pal.muted)
	}
	y += stripOutRowH - 4
	c.rect(frameGut, y-2, w-2*frameGut, 1, pal.grid)
	for i, o := range outs {
		if i == stripMaxOutRow {
			c.text(frameGut, y+2, fmt.Sprintf("+%d more outliers not listed", len(outs)-stripMaxOutRow), fontBold, 12, pal.ink)
			return
		}
		col := pal.statusColor(o.Status)
		c.text(cols[0], y+2, fmt.Sprintf("#%d", o.Seq), fontMono, 12, pal.ink)
		c.text(cols[1], y+2, c.truncate(fontMono, 12, in.display(o.Payload), cols[2]-cols[1]-10), fontMono, 12, pal.ink)
		c.rect(cols[2], y+3, 12, 12, col)
		c.text(cols[2]+18, y+2, fmt.Sprintf("%s %d", statusGlyph(o.Status), o.Status), fontBold, 12, pal.ink)
		c.text(cols[3], y+2, fmt.Sprintf("%d", o.Length), fontSans, 12, pal.ink)
		c.text(cols[4], y+2, fmt.Sprintf("%+d", o.Delta), fontSans, 12, pal.ink)
		c.text(cols[5], y+2, c.truncate(fontSans, 12, strings.Join(o.Reasons, ", "), w-frameGut-cols[5]), fontSans, 12, pal.ink)
		y += stripOutRowH
	}
}

func statusClassLabel(status int) string {
	switch {
	case status <= 0 || status >= 600:
		return "error"
	case isBlockedStatus(status):
		return fmt.Sprintf("%d", status)
	}
	return fmt.Sprintf("%dxx", status/100)
}
