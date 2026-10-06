package preview

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"time"
)

// TimelineRow is one recorded Intruder request. StartUs/EndUs are microsecond
// offsets from run start; both zero means timing was not recorded.
type TimelineRow struct {
	Seq, Worker    int
	StartUs, EndUs int64
	Status, Length int
	Error, Flagged bool
	Matched        bool // response matched the run's grep pattern
	RLHeaders      map[string]string
}

// TimelineInput is the plain-data input of RenderIntruderTimeline. Rows are in
// completion order (as recorded); Seq is dispatch order.
type TimelineInput struct {
	RunID   string
	Attack  string
	Threads int
	DelayMs int
	Target  string
	Rows    []TimelineRow
	Capped  bool
	// Method and Path identify the endpoint (path only, never query values).
	Method, Path string
	// StartedUnixMs is the wall-clock run start; 0 means unknown.
	StartedUnixMs int64
}

const (
	timelineBuckets = 20
	timelineCapNote = "(capped at 2000)"
	tlMaxSeqLanes   = 60 // lanes by Seq when under this many rows
	tlMaxWorkLanes  = 64
	tlLabelW        = 52
	tlBurstWindow   = 5
)

// tlRow is a prepared row.
type tlRow struct {
	TimelineRow
	rank int // completion rank (input order)
}

type tlStats struct {
	sent, ok, blocked, errs, other int
	matched                        int
	retryAfter                     string
	acceptedBefore, inFlightAtBlk  int
	zeroSpan                       bool
	firstBlock                     *tlRow // earliest by EndUs among blocked
	burstAfterSeq                  int    // 0 = none
	burstStartUs                   int64
	p50, p95                       int64 // latency ms
	timed                          bool
	maxUs                          int64
	minSeq, maxSeq                 int
}

func tlIsOK(r TimelineRow) bool  { return !r.Error && r.Status >= 200 && r.Status < 300 }
func tlIsErr(r TimelineRow) bool { return r.Error || r.Status <= 0 }

func tlPrepare(in TimelineInput) (bySeq []tlRow, st tlStats) {
	bySeq = make([]tlRow, len(in.Rows))
	for i, r := range in.Rows {
		bySeq[i] = tlRow{r, i}
		if r.StartUs != 0 || r.EndUs != 0 {
			st.timed = true
		}
	}
	st.zeroSpan = st.timed
	for i := range bySeq {
		r := &bySeq[i]
		if r.EndUs < r.StartUs {
			r.EndUs = r.StartUs
		}
		if r.EndUs > st.maxUs {
			st.maxUs = r.EndUs
		}
		if r.EndUs != r.StartUs {
			st.zeroSpan = false
		}
	}
	sort.SliceStable(bySeq, func(i, j int) bool { return bySeq[i].Seq < bySeq[j].Seq })
	st.sent = len(bySeq)
	if st.sent > 0 {
		st.minSeq, st.maxSeq = bySeq[0].Seq, bySeq[st.sent-1].Seq
	}
	var lat []int64
	for i := range bySeq {
		r := &bySeq[i]
		switch {
		case tlIsErr(r.TimelineRow):
			st.errs++
		case isBlockedStatus(r.Status):
			st.blocked++
		case tlIsOK(r.TimelineRow):
			st.ok++
		default:
			st.other++
		}
		if r.Matched {
			st.matched++
		}
		if st.timed {
			lat = append(lat, (r.EndUs-r.StartUs)/1000)
		}
		if !tlIsErr(r.TimelineRow) && isBlockedStatus(r.Status) {
			fb := st.firstBlock
			if fb == nil || tlBefore(*r, *fb, st.timed) {
				c := *r
				st.firstBlock = &c
			}
		}
	}
	tlBlockContext(bySeq, &st)
	// burst boundary: first Seq after which >50% of the next 5 rows are blocked
	for i := 0; i+tlBurstWindow < len(bySeq); i++ {
		n := 0
		for _, w := range bySeq[i+1 : i+1+tlBurstWindow] {
			if isBlockedStatus(w.Status) && !w.Error {
				n++
			}
		}
		if n*2 > tlBurstWindow {
			st.burstAfterSeq, st.burstStartUs = bySeq[i].Seq, bySeq[i+1].StartUs
			break
		}
	}
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		st.p50, st.p95 = pctl(lat, 50), pctl(lat, 95)
	}
	return bySeq, st
}

// tlBlockContext fills how many requests were accepted (2xx) before the first
// throttle status and how many were still in flight when it returned, plus any
// Retry-After the blocked response carried.
func tlBlockContext(bySeq []tlRow, st *tlStats) {
	fb := st.firstBlock
	if fb == nil {
		return
	}
	for k, v := range fb.RLHeaders {
		if strings.EqualFold(k, "retry-after") && strings.TrimSpace(v) != "" {
			st.retryAfter = strings.TrimSpace(v)
		}
	}
	for _, r := range bySeq {
		if r.Seq == fb.Seq {
			continue
		}
		if st.timed {
			switch {
			case r.EndUs <= fb.EndUs && tlIsOK(r.TimelineRow):
				st.acceptedBefore++
			case r.StartUs <= fb.EndUs && r.EndUs > fb.EndUs:
				st.inFlightAtBlk++
			}
		} else if r.rank < fb.rank && tlIsOK(r.TimelineRow) {
			st.acceptedBefore++
		}
	}
}

// tlBefore: earliest by EndUs when timed, else by completion rank; Seq ties.
func tlBefore(a, b tlRow, timed bool) bool {
	if timed && a.EndUs != b.EndUs {
		return a.EndUs < b.EndUs
	}
	if !timed && a.rank != b.rank {
		return a.rank < b.rank
	}
	return a.Seq < b.Seq
}

func pctl(sorted []int64, p int) int64 {
	idx := (len(sorted)*p + 99) / 100 // nearest rank
	if idx < 1 {
		idx = 1
	}
	return sorted[idx-1]
}

func (st tlStats) summary(capped bool) string {
	s := fmt.Sprintf("%d of %d returned 2xx, %d throttle statuses (429/403/423), %d 5xx/other, %d errors", st.ok, st.sent, st.blocked, st.other, st.errs)
	if st.matched > 0 {
		s += fmt.Sprintf(", %d matched the grep pattern", st.matched)
	}
	if st.retryAfter != "" {
		s += ", Retry-After " + st.retryAfter
	}
	if capped {
		s += " " + timelineCapNote
	}
	return s
}

func threadsText(n int) string {
	if n <= 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

func (st tlStats) alt(in TimelineInput, byWorker bool) string {
	head := fmt.Sprintf("Intruder run %s: %s attack, threads %s, delay %d ms", orDash(in.RunID), orDash(in.Attack), threadsText(in.Threads), in.DelayMs)
	if in.Target != "" {
		head += " against " + in.Target
	}
	if in.Method != "" || in.Path != "" {
		head += " (" + strings.TrimSpace(in.Method+" "+in.Path) + ")"
	}
	parts := []string{head, st.summary(in.Capped)}
	if st.firstBlock != nil {
		fb := st.firstBlock
		if st.timed {
			parts = append(parts, fmt.Sprintf("First throttle status: request #%d returned %d at +%s after %d accepted requests", fb.Seq, fb.Status, fmtUsAsMs(fb.EndUs), st.acceptedBefore))
		} else {
			parts = append(parts, fmt.Sprintf("First throttle status in completion order: request #%d returned %d", fb.Seq, fb.Status))
		}
	} else {
		parts = append(parts, "Observed no throttling status (429/403/423); body-based lockouts are not detected unless a grep pattern was set")
	}
	if st.burstAfterSeq > 0 {
		parts = append(parts, fmt.Sprintf("Throttling burst begins after request #%d", st.burstAfterSeq))
	}
	if st.timed {
		parts = append(parts, fmt.Sprintf("Latency p50 %d ms, p95 %d ms", st.p50, st.p95))
		if byWorker {
			parts = append(parts, "Lanes are grouped by worker")
		}
	} else {
		parts = append(parts, "Timing not recorded for this run; requests are ranked by completion order")
	}
	return AltFromParts(parts...)
}

// fmtUsAsMs renders a microsecond offset as milliseconds with up to one decimal.
func fmtUsAsMs(us int64) string {
	if us%1000 == 0 {
		return fmt.Sprintf("%d ms", us/1000)
	}
	return fmt.Sprintf("%.1f ms", float64(us)/1000)
}

// tlGeom holds all vertical/horizontal geometry, shared by drawing and tests.
type tlGeom struct {
	W                    int
	PlotX0, PlotX1       int
	TilesY               int
	FactsY               int
	ChartTitleY          int
	PlotTop, PlotH       int
	LaneH, Lanes         int
	AxisY                int
	LegendY              int
	SparkTitleY, SparkY  int
	SparkH               int
	BodyH                int
	AxisMaxUs            int64
	Ticks                []float64
	ByWorker             bool
	Strip                bool
	StripCols, StripRows int
	Plotted              int
	Aggregated           int
	Stride               int
	facts                []string
}

const (
	tlTileH  = 52
	tlZone   = 44
	tlSparkH = 64
)

// tlTickLabels formats axis ticks at the precision of the tick step.
func tlTickLabels(g tlGeom) []string {
	step := 1.0
	if len(g.Ticks) > 1 {
		step = g.Ticks[1] - g.Ticks[0]
	}
	out := make([]string, len(g.Ticks))
	for i, t := range g.Ticks {
		out[i] = formatTick(t, step)
	}
	return out
}

// XOf maps a microsecond offset onto the plot x coordinate.
func (g tlGeom) XOf(us int64) int {
	if g.AxisMaxUs <= 0 {
		return g.PlotX0
	}
	if us > g.AxisMaxUs {
		us = g.AxisMaxUs
	}
	return g.PlotX0 + int(float64(us)/float64(g.AxisMaxUs)*float64(g.PlotX1-g.PlotX0)+0.5)
}

func tlLaneCount(rows []tlRow, in TimelineInput, plotted int) (lanes int, byWorker bool) {
	if in.Threads >= 1 && len(rows) > tlMaxSeqLanes {
		maxW := 0
		for _, r := range rows {
			if r.Worker > maxW {
				maxW = r.Worker
			}
		}
		if maxW > 0 {
			if maxW > tlMaxWorkLanes {
				maxW = tlMaxWorkLanes
			}
			return maxW, true
		}
	}
	if plotted > tlMaxSeqLanes {
		return tlMaxSeqLanes, false
	}
	return plotted, false
}

func tlLaneH(lanes int) int {
	switch {
	case lanes <= 20:
		return 18
	case lanes <= 40:
		return 14
	}
	return 12
}

func tlFacts(in TimelineInput, st tlStats, bySeq []tlRow) []string {
	f1 := fmt.Sprintf("attack %s  |  threads %s  |  delay %d ms  |  target %s", orDash(in.Attack), threadsText(in.Threads), in.DelayMs, orDash(in.Target))
	if ep := strings.TrimSpace(in.Method + " " + in.Path); ep != "" {
		f1 += "  |  " + ep
	}
	if in.Capped {
		f1 += "  |  " + timelineCapNote
	}
	lines := []string{f1}
	if l := tlTimingLine(in, st); l != "" {
		lines = append(lines, l)
	}
	if l := tlBlockLine(st); l != "" {
		lines = append(lines, l)
	}
	if h := tlRLLine(st, bySeq); h != "" {
		lines = append(lines, h)
	}
	if st.timed {
		if g := tlGapMs(bySeq); g != "" {
			lines = append(lines, g)
		}
	}
	return lines
}

func fmtDurationUs(us int64) string {
	if us < 1000000 {
		return fmtUsAsMs(us)
	}
	return fmt.Sprintf("%.1f s", float64(us)/1e6)
}

// tlTimingLine reports wall-clock start, duration and achieved request rate.
func tlTimingLine(in TimelineInput, st tlStats) string {
	var parts []string
	if in.StartedUnixMs > 0 {
		parts = append(parts, "started "+time.UnixMilli(in.StartedUnixMs).UTC().Format("2006-01-02 15:04:05")+" UTC")
	}
	if st.timed && st.maxUs > 0 {
		parts = append(parts, "duration "+fmtDurationUs(st.maxUs))
		parts = append(parts, fmt.Sprintf("%.1f req/s achieved (client side)", float64(st.sent)*1e6/float64(st.maxUs)))
	}
	return strings.Join(parts, "  |  ")
}

// tlBlockLine states how many requests were accepted before the first throttle.
func tlBlockLine(st tlStats) string {
	fb := st.firstBlock
	if fb == nil {
		return ""
	}
	if !st.timed {
		return fmt.Sprintf("first throttle status %d at request #%d after %d accepted (2xx) requests in completion order", fb.Status, fb.Seq, st.acceptedBefore)
	}
	return fmt.Sprintf("first block after %d accepted (2xx) requests, %d in flight  |  time to first block +%s  |  status %d at request #%d",
		st.acceptedBefore, st.inFlightAtBlk, fmtUsAsMs(fb.EndUs), fb.Status, fb.Seq)
}

// tlRLLine lists recorded rate-limit headers from the first block row, else
// the first row carrying any.
func tlRLLine(st tlStats, bySeq []tlRow) string {
	var src *tlRow
	if st.firstBlock != nil && len(st.firstBlock.RLHeaders) > 0 {
		src = st.firstBlock
	}
	for i := range bySeq {
		if src == nil && len(bySeq[i].RLHeaders) > 0 {
			src = &bySeq[i]
		}
	}
	if src == nil {
		return ""
	}
	keys := make([]string, 0, len(src.RLHeaders))
	for k := range src.RLHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []string
	for _, k := range keys {
		v := src.RLHeaders[k]
		if len(v) > 40 {
			v = v[:40] + "..."
		}
		b = append(b, k+": "+v)
		if len(b) == 4 {
			break
		}
	}
	return fmt.Sprintf("recorded headers on request #%d  |  %s", src.Seq, strings.Join(b, ", "))
}

// tlGapMs reports the median recorded dispatch gap between consecutive Seq.
func tlGapMs(bySeq []tlRow) string {
	var gaps []int64
	for i := 1; i < len(bySeq); i++ {
		if g := bySeq[i].StartUs - bySeq[i-1].StartUs; g >= 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return ""
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return "recorded dispatch gap (median between consecutive requests): " + fmtUsAsMs(gaps[len(gaps)/2])
}

func timelineGeometry(in TimelineInput, bySeq []tlRow, st tlStats, o Opts, rowBudget int) tlGeom {
	w := o.width()
	g := tlGeom{W: w, PlotX0: frameGut + tlLabelW, PlotX1: w - frameGut - 6}
	g.Plotted = len(bySeq)
	if g.Plotted > rowBudget {
		g.Plotted = rowBudget
	}
	g.Aggregated = len(bySeq) - g.Plotted
	g.Stride = 1
	if g.Aggregated > 0 {
		g.Stride = (len(bySeq) + g.Plotted - 1) / g.Plotted
		g.Plotted = (len(bySeq) + g.Stride - 1) / g.Stride
		g.Aggregated = len(bySeq) - g.Plotted
	}
	g.facts = tlFacts(in, st, bySeq)
	g.TilesY = 12
	g.FactsY = g.TilesY + tlTileH + 10
	g.ChartTitleY = g.FactsY + 18*len(g.facts) + 8
	if !st.timed || len(bySeq) == 0 {
		g.Strip = true
		g.PlotTop = g.ChartTitleY + 40 // title + note
		cell := 22
		g.StripCols = (g.PlotX1 - g.PlotX0) / cell
		if g.StripCols < 1 {
			g.StripCols = 1
		}
		g.StripRows = (g.Plotted + g.StripCols - 1) / g.StripCols
		if g.StripRows < 1 {
			g.StripRows = 1
		}
		g.PlotH = g.StripRows * cell
		g.LaneH = cell
		g.LegendY = g.PlotTop + g.PlotH + 30
	} else {
		g.Lanes, g.ByWorker = tlLaneCount(bySeq, in, g.Plotted)
		if g.Lanes < 1 {
			g.Lanes = 1
		}
		g.LaneH = tlLaneH(g.Lanes)
		g.PlotTop = g.ChartTitleY + 20 + tlZone
		g.PlotH = g.Lanes * g.LaneH
		ms := float64(st.maxUs) / 1000
		if st.zeroSpan {
			ms++ // zero-duration run: leave a visible window right of the bars
		}
		g.Ticks = niceTicks(0, ms, 6)
		g.AxisMaxUs = int64(g.Ticks[len(g.Ticks)-1]*1000 + 0.5)
		g.AxisY = g.PlotTop + g.PlotH + 4
		g.LegendY = g.AxisY + 44
	}
	g.SparkTitleY = g.LegendY + 50
	g.SparkY = g.SparkTitleY + 22
	g.SparkH = tlSparkH
	g.BodyH = g.SparkY + g.SparkH + 28
	return g
}

// RenderIntruderTimeline draws the request timeline (waterfall lanes) render.
func RenderIntruderTimeline(in TimelineInput, o Opts) (Rendered, error) {
	bySeq, st := tlPrepare(in)
	var final tlGeom
	f := frame{
		Title:      "Intruder request timeline (waterfall lanes)",
		Provenance: tlProvenance(in, st),
		BodyHeight: func(rows int) int {
			return timelineGeometry(in, bySeq, st, o, rows).BodyH
		},
		Draw: func(c *canvas, body image.Rectangle, rows int) {
			g := timelineGeometry(in, bySeq, st, o, rows)
			final = g
			drawTimeline(c, body.Min.Y, g, in, bySeq, st)
		},
	}
	data, w, h, err := renderFrame(o, f)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{PNG: data, Alt: st.alt(in, final.ByWorker), Summary: st.summary(in.Capped), Kind: KindIntruderTimeline, Width: w, Height: h}, nil
}

func tlProvenance(in TimelineInput, st tlStats) string {
	s := "run " + orDash(in.RunID)
	if st.sent > 0 {
		s += fmt.Sprintf("  |  requests #%d-#%d", st.minSeq, st.maxSeq)
	}
	return s
}

func drawTimeline(c *canvas, top int, g tlGeom, in TimelineInput, bySeq []tlRow, st tlStats) {
	p := c.pal
	x0 := frameGut
	tiles := []struct {
		l, v string
		c    color.RGBA
	}{
		{"Requests sent", fmt.Sprint(st.sent), p.accent},
		{"2xx responses", fmt.Sprint(st.ok), p.success},
		{"Throttle 429/403/423", fmt.Sprint(st.blocked), p.blocked},
		{"5xx / other", fmt.Sprint(st.other), p.server},
		{"Errors", fmt.Sprint(st.errs), p.errc},
	}
	if st.matched > 0 {
		tiles = append(tiles, struct {
			l, v string
			c    color.RGBA
		}{"Grep pattern matches", fmt.Sprint(st.matched), p.client})
	}
	tw := (g.W - 2*frameGut - (len(tiles)-1)*12) / len(tiles)
	for i, t := range tiles {
		c.statTile(x0+i*(tw+12), top+g.TilesY, tw, tlTileH, t.l, t.v, t.c)
	}
	for i, l := range g.facts {
		c.text(x0, top+g.FactsY+i*18, c.truncate(fontSans, 12, l, g.W-2*frameGut), fontSans, 12, p.muted)
	}
	if g.Strip {
		drawTimelineStrip(c, top, g, bySeq, st)
	} else {
		drawTimelineLanes(c, top, g, in, bySeq, st)
	}
	drawTimelineLegend(c, top, g, st)
	drawTimelineSpark(c, top, g, bySeq, st)
}

func tlLaneOf(g tlGeom, r tlRow, idx, total int) int {
	if g.ByWorker {
		l := r.Worker - 1
		if l < 0 {
			l = 0
		}
		if l >= g.Lanes {
			l = g.Lanes - 1
		}
		return l
	}
	if total <= g.Lanes {
		return idx
	}
	return idx * g.Lanes / total
}

func drawTimelineLanes(c *canvas, top int, g tlGeom, in TimelineInput, bySeq []tlRow, st tlStats) {
	p := c.pal
	pt := top + g.PlotTop
	mode := "one lane per request"
	if g.ByWorker {
		mode = "one lane per worker"
	} else if g.Lanes < len(bySeq) {
		mode = "requests grouped into lanes by Seq"
	}
	c.text(frameGut, top+g.ChartTitleY, "Request timeline  (x = ms from run start, "+mode+")", fontBold, 13, p.ink)
	if g.Aggregated > 0 {
		c.text(frameGut, top+g.ChartTitleY+18-2, fmt.Sprintf("+%d more aggregated (every %d requests plotted; counts above include all)", g.Aggregated, g.Stride), fontSans, 12, p.muted)
	}
	c.rect(g.PlotX0, pt, g.PlotX1-g.PlotX0+1, g.PlotH, p.panel)
	// grid + axis
	labels := tlTickLabels(g)
	for i, t := range g.Ticks {
		x := g.XOf(int64(t*1000 + 0.5))
		c.rect(x, pt, 1, g.PlotH, p.grid)
		lbl := labels[i]
		c.text(x-c.measure(fontSans, 11, lbl)/2, top+g.AxisY+4, lbl, fontSans, 11, p.muted)
	}
	c.rect(g.PlotX0, pt+g.PlotH, g.PlotX1-g.PlotX0+1, 1, p.muted)
	c.textRight(g.PlotX1, top+g.AxisY+20, "time since run start (ms)", fontSans, 11, p.muted)
	// lane labels
	laneLbl := func(l int) string {
		if g.ByWorker {
			return fmt.Sprintf("W%d", l+1)
		}
		return ""
	}
	// bars
	plotted := make([]tlRow, 0, g.Plotted)
	for i := 0; i < len(bySeq); i += g.Stride {
		plotted = append(plotted, bySeq[i])
	}
	if st.firstBlock != nil && g.Stride > 1 {
		found := false
		for _, r := range plotted {
			found = found || r.Seq == st.firstBlock.Seq
		}
		if !found {
			plotted = append(plotted, *st.firstBlock)
		}
	}
	bh := g.LaneH - 4
	glyphSize := 10
	for i, r := range plotted {
		l := tlLaneOf(g, r, i, len(plotted))
		y := pt + l*g.LaneH + 2
		xa, xb := g.XOf(r.StartUs), g.XOf(r.EndUs)
		if xb-xa < 3 {
			xb = xa + 3
		}
		if xb > g.PlotX1 {
			xb = g.PlotX1
			if xa > xb-3 {
				xa = xb - 3
			}
		}
		col := p.statusColor(r.Status)
		if tlIsErr(r.TimelineRow) {
			col = p.errc
		}
		c.rect(xa, y, xb-xa, bh, col)
		gl := statusGlyph(r.Status)
		if tlIsErr(r.TimelineRow) {
			gl = "E"
		}
		gw := c.measure(fontBold, glyphSize, gl)
		if xb-xa >= gw+6 && bh >= 10 {
			c.text(xa+(xb-xa-gw)/2, y+(bh-12)/2, gl, fontBold, glyphSize, p.paper)
		} else if gl != "ok" && gl != "3" {
			c.text(xb+2, y+(bh-12)/2, gl, fontBold, glyphSize, col)
		}
		if r.Flagged {
			c.strokeRect(xa-1, y-1, xb-xa+2, bh+2, p.ink)
		}
	}
	// lane labels (after bars so they are never covered)
	step := 1
	if g.Lanes > 20 {
		step = 5
	}
	for l := 0; l < g.Lanes; l += step {
		lbl := laneLbl(l)
		if lbl == "" {
			a := l * len(plotted) / g.Lanes
			if g.Lanes >= len(plotted) {
				a = l
			}
			if a < len(plotted) {
				lbl = fmt.Sprintf("#%d", plotted[a].Seq)
			}
		}
		c.textRight(g.PlotX0-6, pt+l*g.LaneH+(g.LaneH-12)/2, lbl, fontSans, 11, p.muted)
	}
	// rules
	if st.firstBlock != nil {
		fb := st.firstBlock
		x := g.XOf(fb.EndUs)
		c.rect(x, pt-tlZone+6, 2, g.PlotH+tlZone-6, p.blocked)
		txt := fmt.Sprintf("first %d at request #%d, +%s", fb.Status, fb.Seq, fmtUsAsMs(fb.EndUs))
		tlRuleLabel(c, g, x, pt-tlZone+4, txt, p.blocked)
	}
	if st.burstAfterSeq > 0 {
		x := g.XOf(st.burstStartUs)
		for y := pt - 20; y < pt+g.PlotH; y += 6 {
			c.rect(x, y, 2, 3, p.client)
		}
		tlRuleLabel(c, g, x, pt-20, fmt.Sprintf("burst boundary: throttling starts after #%d", st.burstAfterSeq), p.client)
	}
}

func tlRuleLabel(c *canvas, g tlGeom, x, y int, txt string, col color.RGBA) {
	w := c.measure(fontBold, 12, txt)
	lx := x + 6
	if lx+w > g.PlotX1 {
		lx = x - 6 - w
	}
	if lx < g.PlotX0 {
		lx = g.PlotX0
	}
	c.rect(lx-2, y-1, w+4, 17, c.pal.paper)
	c.text(lx, y, txt, fontBold, 12, col)
}

func drawTimelineStrip(c *canvas, top int, g tlGeom, bySeq []tlRow, st tlStats) {
	p := c.pal
	c.text(frameGut, top+g.ChartTitleY, "Requests ranked by completion order", fontBold, 13, p.ink)
	note := "timing not recorded for this run: order only, no time axis, no latency or burst timing"
	c.text(frameGut, top+g.ChartTitleY+18, note, fontBold, 12, p.client)
	if len(bySeq) == 0 {
		c.text(g.PlotX0, top+g.PlotTop+2, "no requests recorded", fontSans, 12, p.muted)
		return
	}
	ranked := append([]tlRow(nil), bySeq...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].rank < ranked[j].rank })
	var plotted []tlRow
	for i := 0; i < len(ranked); i += g.Stride {
		plotted = append(plotted, ranked[i])
	}
	if g.Aggregated > 0 {
		c.text(g.PlotX0, top+g.PlotTop-16, fmt.Sprintf("+%d more aggregated (every %d plotted)", g.Aggregated, g.Stride), fontSans, 12, p.muted)
	}
	cell := g.LaneH
	for i, r := range plotted {
		cx := g.PlotX0 + (i%g.StripCols)*cell
		cy := top + g.PlotTop + (i/g.StripCols)*cell
		col := p.statusColor(r.Status)
		gl := statusGlyph(r.Status)
		if tlIsErr(r.TimelineRow) {
			col, gl = p.errc, "E"
		}
		c.rect(cx, cy, cell-2, cell-2, col)
		if st.firstBlock != nil && r.Seq == st.firstBlock.Seq {
			c.strokeRect(cx-1, cy-1, cell, cell, p.ink)
		}
		c.text(cx+(cell-2-c.measure(fontBold, 10, gl))/2, cy+(cell-2-12)/2, gl, fontBold, 10, p.paper)
	}
	if st.firstBlock != nil {
		c.text(g.PlotX0, top+g.PlotTop+g.PlotH+2, fmt.Sprintf("outlined cell: first %d in completion order, request #%d", st.firstBlock.Status, st.firstBlock.Seq), fontSans, 12, p.ink)
	}
}

func drawTimelineLegend(c *canvas, top int, g tlGeom, st tlStats) {
	p := c.pal
	items := []legendItem{
		{"2xx ok", p.success}, {"3xx", p.redirect}, {"4xx", p.client},
		{"throttle 429/403/423 X", p.blocked}, {"5xx", p.server}, {"error E", p.errc},
	}
	y := top + g.LegendY
	h := c.legend(frameGut, y, g.W-frameGut, items)
	extra := "outlined bar = flagged by grep"
	if !g.Strip {
		extra = "red rule = first throttle status; dashed amber = burst boundary; " + extra
	}
	c.text(frameGut, y+h, extra, fontSans, 12, p.muted)
}

// tlBucket is one outcome-mix bucket.
type tlBucket struct{ n, ok, bl, ot int }

// tlBucketize groups rows into outcome buckets: up to timelineBuckets time
// windows by completion time, or min(20, rows) dense rank buckets when timing
// was not recorded so no bucket is spuriously empty.
func tlBucketize(bySeq []tlRow, st tlStats) []tlBucket {
	nb := timelineBuckets
	timed := st.timed && st.maxUs > 0
	if !timed {
		if len(bySeq) < nb {
			nb = len(bySeq)
		}
		if nb == 0 {
			return nil
		}
	}
	out := make([]tlBucket, nb)
	for i, r := range bySeq {
		var b int
		if timed {
			b = int(r.EndUs * int64(nb) / (st.maxUs + 1))
		} else {
			b = i * nb / len(bySeq)
		}
		if b >= nb {
			b = nb - 1
		}
		out[b].n++
		switch {
		case tlIsErr(r.TimelineRow):
			out[b].ot++
		case isBlockedStatus(r.Status):
			out[b].bl++
		case tlIsOK(r.TimelineRow):
			out[b].ok++
		default:
			out[b].ot++
		}
	}
	return out
}

func tlSparkTitle(st tlStats) string {
	if st.timed && st.maxUs > 0 {
		return "Outcome mix by completion time (20 buckets; \"none\" = no completions): teal = 2xx, red = throttle status, grey = other"
	}
	return "Outcome mix by completion rank: teal = 2xx, red = throttle status, grey = other"
}

func drawTimelineSpark(c *canvas, top int, g tlGeom, bySeq []tlRow, st tlStats) {
	p := c.pal
	buckets := tlBucketize(bySeq, st)
	c.text(frameGut, top+g.SparkTitleY, c.truncate(fontBold, 13, tlSparkTitle(st), g.W-2*frameGut), fontBold, 13, p.ink)
	y0 := top + g.SparkY
	x0, x1 := g.PlotX0, g.PlotX1
	c.rect(x0, y0, x1-x0+1, g.SparkH, p.panel)
	c.text(x0-6-c.measure(fontSans, 11, "100%"), y0-2, "100%", fontSans, 11, p.muted)
	c.text(x0-6-c.measure(fontSans, 11, "0%"), y0+g.SparkH-12, "0%", fontSans, 11, p.muted)
	if len(buckets) > 0 {
		bw := (x1 - x0) / len(buckets)
		for b, k := range buckets {
			bx := x0 + b*bw
			if k.n == 0 {
				c.rect(bx+2, y0+g.SparkH-2, bw-4, 2, p.grid)
				if lw := c.measure(fontSans, 10, "none"); bw-4 >= lw {
					c.text(bx+(bw-lw)/2, y0+g.SparkH-18, "none", fontSans, 10, p.muted)
				}
				continue
			}
			hs := g.SparkH * k.ok / k.n
			hb := g.SparkH * k.bl / k.n
			ho := g.SparkH * k.ot / k.n
			c.rect(bx+2, y0+g.SparkH-hs, bw-4, hs, p.success)
			c.rect(bx+2, y0+g.SparkH-hs-hb, bw-4, hb, p.blocked)
			c.rect(bx+2, y0+g.SparkH-hs-hb-ho, bw-4, ho, p.errc)
		}
	}
	c.rect(x0, y0+g.SparkH, x1-x0+1, 1, p.muted)
	if st.timed {
		labels := tlTickLabels(g)
		c.text(x0, y0+g.SparkH+4, "0 ms", fontSans, 11, p.muted)
		c.textRight(x1, y0+g.SparkH+4, labels[len(labels)-1]+" ms", fontSans, 11, p.muted)
	}
}
