package preview

import (
	"fmt"
	"image"
	"sort"
	"strconv"
	"strings"
)

// WaterfallRow is one flow in a timing sequence. StartMs is the epoch
// millisecond request start (Flow.TS); DurationMs is the total recorded time.
type WaterfallRow struct {
	FlowID     int64
	Method     string
	Path       string
	Status     int
	StartMs    int64
	DurationMs int
}

// WaterfallInput is plain data for RenderFlowWaterfall.
type WaterfallInput struct {
	Title string
	Rows  []WaterfallRow
}

const (
	wfRowH      = 18
	wfHeaderH   = 34
	wfAxisH     = 26
	wfNoteH     = 52
	wfChipW     = 62
	wfLabelMax  = 300
	wfDurW      = 84
	wfBarH      = 10
	wfMinBarPx  = 2
	wfFootnote  = "Only total duration (ms precision) is recorded per flow; DNS, connect, TLS and time-to-first-byte phases are not recorded, so no phase split is drawn."
	wfEmptyText = "No flows recorded for this sequence."
)

type unit struct {
	name string
	div  float64
}

// axisUnit picks ms, or seconds when the range exceeds 5000 ms.
func axisUnit(rangeMs int64) unit {
	if rangeMs > 5000 {
		return unit{"s", 1000}
	}
	return unit{"ms", 1}
}

// wfGeom is the lane geometry shared by drawing and tests.
type wfGeom struct {
	labelX, labelW int
	laneX, laneW   int
	durRight       int
	topY           int // y of first row, relative to image top
	rowH           int
	axisMax        float64 // ms, last tick
	ticks          []float64
	unit           unit
	rangeMs        int64
	plotted        int
	hidden         int
}

func (g wfGeom) xAt(ms float64) int {
	if g.axisMax <= 0 {
		return g.laneX
	}
	return g.laneX + int(ms/g.axisMax*float64(g.laneW))
}

func (g wfGeom) rowY(i int) int { return g.topY + i*g.rowH }

func sortedWaterfall(rows []WaterfallRow) []WaterfallRow {
	out := append([]WaterfallRow(nil), rows...)
	for i := range out {
		if out[i].DurationMs < 0 {
			out[i].DurationMs = 0
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].FlowID < out[j].FlowID
	})
	return out
}

func wfRange(rows []WaterfallRow) int64 {
	if len(rows) == 0 {
		return 0
	}
	base := rows[0].StartMs
	var end int64
	for _, r := range rows {
		if e := r.StartMs - base + int64(r.DurationMs); e > end {
			end = e
		}
	}
	return end
}

// waterfallGeometry computes the layout for the first render attempt.
func waterfallGeometry(in WaterfallInput, o Opts) wfGeom {
	return wfLayout(sortedWaterfall(in.Rows), o.width(), o.maxRows())
}

func wfLayout(rows []WaterfallRow, w, budget int) wfGeom {
	g := wfGeom{rowH: wfRowH}
	g.labelX = frameGut + wfChipW + 8
	g.labelW = wfLabelMax
	if w < 900 {
		g.labelW = 220
	}
	g.durRight = w - frameGut
	g.laneX = g.labelX + g.labelW + 12
	g.laneW = g.durRight - wfDurW - g.laneX
	g.rangeMs = wfRange(rows)
	g.unit = axisUnit(g.rangeMs)
	g.ticks = niceTicks(0, float64(g.rangeMs)/g.unit.div, 6)
	g.axisMax = g.ticks[len(g.ticks)-1] * g.unit.div
	// vertical budget: leave room for chrome, header, axis, aggregate row, note
	fit := (maxRenderHeight - titleH - footerH - wfHeaderH - wfAxisH - wfNoteH - wfRowH) / wfRowH
	limit := budget
	if fit < limit {
		limit = fit
	}
	g.plotted = len(rows)
	if g.plotted > limit {
		g.plotted = limit
	}
	g.hidden = len(rows) - g.plotted
	g.topY = titleH + wfHeaderH + wfAxisH
	return g
}

func wfBodyHeight(g wfGeom) int {
	n := g.plotted
	if g.hidden > 0 {
		n++
	}
	if n == 0 {
		n = 1
	}
	return wfHeaderH + wfAxisH + n*wfRowH + wfNoteH
}

func fmtMs(ms int64) string { return strconv.FormatInt(ms, 10) + " ms" }

func fmtTick(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func wfPathLabel(r WaterfallRow) string {
	p := r.Path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i] + "?..."
	}
	return "#" + strconv.FormatInt(r.FlowID, 10) + " " + p
}

func wfSummary(rows []WaterfallRow) string {
	if len(rows) == 0 {
		return "No flows recorded"
	}
	noun := "flows"
	if len(rows) == 1 {
		noun = "flow"
	}
	return fmt.Sprintf("%d %s over %s, ordered by request start", len(rows), noun, fmtMs(wfRange(rows)))
}

func wfAlt(in WaterfallInput, rows []WaterfallRow, g wfGeom) string {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Flow timing waterfall"
	}
	if len(rows) == 0 {
		return AltFromParts(title+": no flows recorded", "Only total duration is recorded per flow")
	}
	var order []string
	for i, r := range rows {
		if i >= 8 {
			break
		}
		order = append(order, fmt.Sprintf("#%d %s %s (%d, +%d ms, %d ms)", r.FlowID, r.Method, strings.SplitN(r.Path, "?", 2)[0], r.Status, r.StartMs-rows[0].StartMs, r.DurationMs))
	}
	parts := []string{
		fmt.Sprintf("%s: %d flows across %s", title, len(rows), fmtMs(g.rangeMs)),
		"Order by start: " + strings.Join(order, "; "),
	}
	if g.hidden > 0 {
		parts = append(parts, fmt.Sprintf("%d flows plotted, +%d more aggregated into one bar", g.plotted, g.hidden))
	}
	slow := rows[0]
	for _, r := range rows {
		if r.DurationMs > slow.DurationMs {
			slow = r
		}
	}
	parts = append(parts, fmt.Sprintf("Slowest is #%d at %d ms", slow.FlowID, slow.DurationMs),
		"Only total duration is recorded per flow; no phase split is shown")
	return AltFromParts(parts...)
}

func wfProvenance(rows []WaterfallRow) string {
	if len(rows) == 0 {
		return ""
	}
	lo, hi := rows[0].FlowID, rows[0].FlowID
	for _, r := range rows {
		if r.FlowID < lo {
			lo = r.FlowID
		}
		if r.FlowID > hi {
			hi = r.FlowID
		}
	}
	return fmt.Sprintf("Flow ids %d-%d, %d flows, ms precision, offsets from first request start", lo, hi, len(rows))
}

// RenderFlowWaterfall draws recorded flow order and total durations on a
// shared millisecond axis. Rows sort by StartMs then FlowID.
func RenderFlowWaterfall(in WaterfallInput, o Opts) (Rendered, error) {
	rows := sortedWaterfall(in.Rows)
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Flow timing waterfall"
	}
	var last wfGeom
	data, w, h, err := renderFrame(o, frame{
		Title:      title,
		Provenance: wfProvenance(rows),
		BodyHeight: func(budget int) int {
			last = wfLayout(rows, o.width(), budget)
			return wfBodyHeight(last)
		},
		Draw: func(c *canvas, body image.Rectangle, budget int) {
			last = wfLayout(rows, body.Dx(), budget)
			drawWaterfall(c, body, rows, last)
		},
	})
	if err != nil {
		return Rendered{}, err
	}
	g := last
	return Rendered{PNG: data, Alt: wfAlt(in, rows, g), Summary: wfSummary(rows), Kind: KindFlowWaterfall, Width: w, Height: h}, nil
}

func drawWaterfall(c *canvas, body image.Rectangle, rows []WaterfallRow, g wfGeom) {
	pal := c.pal
	c.text(frameGut, body.Min.Y+9, wfSummary(rows), fontBold, 13, pal.ink)
	axisY := body.Min.Y + wfHeaderH
	if len(rows) == 0 {
		c.text(frameGut, axisY+8, wfEmptyText, fontSans, 13, pal.muted)
		drawWfNote(c, body, 1)
		return
	}
	n := g.plotted
	if g.hidden > 0 {
		n++
	}
	laneBottom := g.rowY(n)
	// axis and gridlines
	c.rect(g.laneX, axisY+wfAxisH-1, g.laneW+1, 1, pal.muted)
	for _, t := range g.ticks {
		x := g.xAt(t * g.unit.div)
		c.rect(x, axisY+wfAxisH-5, 1, 5, pal.muted)
		c.rect(x, g.topY, 1, laneBottom-g.topY, pal.grid)
		lbl := fmtTick(t)
		tw := c.measure(fontSans, 11, lbl)
		c.text(x-tw/2, axisY+4, lbl, fontSans, 11, pal.muted)
	}
	c.text(frameGut, axisY+4, "offset ("+g.unit.name+")", fontSans, 11, pal.muted)
	base := rows[0].StartMs
	for i := 0; i < g.plotted; i++ {
		r := rows[i]
		y := g.rowY(i)
		if i%2 == 1 {
			c.rect(frameGut, y, g.durRight-frameGut, g.rowH, pal.panel)
			for _, t := range g.ticks {
				c.rect(g.xAt(t*g.unit.div), y, 1, g.rowH, pal.grid)
			}
		}
		col := pal.statusColor(r.Status)
		c.rect(frameGut, y+2, wfChipW, g.rowH-4, col)
		chip := strconv.Itoa(r.Status) + " " + statusGlyph(r.Status)
		if r.Status <= 0 {
			chip = "ERR E"
		}
		c.text(frameGut+5, y+3, chip, fontBold, 11, pal.paper)
		mw := c.text(g.labelX, y+3, r.Method, fontBold, 11, pal.ink)
		c.text(g.labelX+mw+6, y+3, c.truncate(fontSans, 11, wfPathLabel(r), g.labelW-mw-6), fontSans, 11, pal.ink)
		off := float64(r.StartMs - base)
		x0 := g.xAt(off)
		x1 := g.xAt(off + float64(r.DurationMs))
		bw := x1 - x0
		if bw < wfMinBarPx {
			bw = wfMinBarPx
		}
		c.rect(x0, y+(g.rowH-wfBarH)/2, bw, wfBarH, col)
		c.textRight(g.durRight, y+3, fmtMs(int64(r.DurationMs)), fontSans, 11, pal.ink)
	}
	if g.hidden > 0 {
		y := g.rowY(g.plotted)
		rest := rows[g.plotted:]
		lo := float64(rest[0].StartMs - base)
		var hi float64
		for _, r := range rest {
			if e := float64(r.StartMs-base) + float64(r.DurationMs); e > hi {
				hi = e
			}
		}
		c.rect(frameGut, y+2, wfChipW, g.rowH-4, pal.muted)
		c.text(frameGut+5, y+3, "more", fontBold, 11, pal.paper)
		c.text(g.labelX, y+3, c.truncate(fontBold, 11, fmt.Sprintf("+%d more aggregated", g.hidden), g.labelW), fontBold, 11, pal.ink)
		x0, x1 := g.xAt(lo), g.xAt(hi)
		for x := x0; x <= x1; x += 4 {
			c.rect(x, y+(g.rowH-wfBarH)/2, 2, wfBarH, pal.muted)
		}
		c.textRight(g.durRight, y+3, fmt.Sprintf("%.0f-%.0f ms", lo, hi), fontSans, 11, pal.ink)
	}
	drawWfNote(c, body, n)
}

func drawWfNote(c *canvas, body image.Rectangle, n int) {
	y := body.Min.Y + wfHeaderH + wfAxisH + n*wfRowH + 10
	for i, ln := range c.wrap(fontSans, 12, wfFootnote, body.Dx()-2*frameGut, 2) {
		c.text(frameGut, y+i*16, ln, fontSans, 12, c.pal.muted)
	}
}
