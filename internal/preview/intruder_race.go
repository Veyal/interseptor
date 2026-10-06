package preview

import (
	"fmt"
	"image"
	"sort"
	"strconv"
	"strings"
)

// RaceRow is one recorded response of a repeat/race run.
type RaceRow struct {
	Seq, Worker    int
	StartUs, EndUs int64 // microsecond offsets from run start; 0/0 = not recorded
	Status, Length int
	BodyHash       string
	Matched        bool
	Extracted      []string
}

// RaceInput is the plain-data input for RenderIntruderRace.
type RaceInput struct {
	RunID        string
	Threads      int
	Barrier      bool
	Rows         []RaceRow
	SuccessLabel string // grep text used to mark success
}

const raceFooterNote = "separate connections; not single-packet synchronisation"

type raceGroup struct {
	Status, Length int
	Hash           string
	Count          int
	Matched        int
	Sample         string
	FirstSeq       int
}

type raceDup struct {
	Value string
	Count int
}

type raceStats struct {
	HasTiming   bool
	SpreadUs    int64
	MinStartUs  int64
	MaxStartUs  int64
	MaxEndUs    int64
	Groups      []raceGroup
	Dups        []raceDup // values seen in 2+ responses, largest first
	Matched     int
	First, Last *RaceRow // by EndUs ordering; nil without timing
	Banner      string
	dupRows     map[int]bool
}

func rowHasTiming(r RaceRow) bool { return r.EndUs > 0 && r.EndUs >= r.StartUs }

func analyzeRace(in RaceInput) raceStats {
	st := raceStats{dupRows: map[int]bool{}}
	groups := map[string]*raceGroup{}
	valRows := map[string][]int{} // value -> row indexes
	for i, r := range in.Rows {
		key := fmt.Sprintf("%d|h:%s", r.Status, r.BodyHash)
		if r.BodyHash == "" {
			key = fmt.Sprintf("%d|l:%d", r.Status, r.Length)
		}
		g := groups[key]
		if g == nil {
			g = &raceGroup{Status: r.Status, Length: r.Length, Hash: r.BodyHash, FirstSeq: r.Seq}
			groups[key] = g
		}
		g.Count++
		if r.Seq < g.FirstSeq {
			g.FirstSeq = r.Seq
		}
		if r.Matched {
			g.Matched++
			st.Matched++
		}
		seen := map[string]bool{}
		for _, v := range r.Extracted {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			valRows[v] = append(valRows[v], i)
			if g.Sample == "" {
				g.Sample = v
			}
		}
		if rowHasTiming(r) {
			if !st.HasTiming {
				st.HasTiming, st.MinStartUs, st.MaxStartUs = true, r.StartUs, r.StartUs
			}
			if r.StartUs < st.MinStartUs {
				st.MinStartUs = r.StartUs
			}
			if r.StartUs > st.MaxStartUs {
				st.MaxStartUs = r.StartUs
			}
			if r.EndUs > st.MaxEndUs {
				st.MaxEndUs = r.EndUs
			}
		}
	}
	for _, g := range groups {
		st.Groups = append(st.Groups, *g)
	}
	sort.Slice(st.Groups, func(i, j int) bool {
		a, b := st.Groups[i], st.Groups[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.FirstSeq < b.FirstSeq
	})
	for v, idx := range valRows {
		if len(idx) >= 2 {
			st.Dups = append(st.Dups, raceDup{Value: v, Count: len(idx)})
			for _, i := range idx {
				st.dupRows[i] = true
			}
		}
	}
	sort.Slice(st.Dups, func(i, j int) bool {
		if st.Dups[i].Count != st.Dups[j].Count {
			return st.Dups[i].Count > st.Dups[j].Count
		}
		return st.Dups[i].Value < st.Dups[j].Value
	})
	if st.HasTiming {
		st.SpreadUs = st.MaxStartUs - st.MinStartUs
		var timed []RaceRow
		for _, r := range in.Rows {
			if rowHasTiming(r) {
				timed = append(timed, r)
			}
		}
		sort.SliceStable(timed, func(i, j int) bool {
			if timed[i].EndUs != timed[j].EndUs {
				return timed[i].EndUs < timed[j].EndUs
			}
			return timed[i].Seq < timed[j].Seq
		})
		f, l := timed[0], timed[len(timed)-1]
		st.First, st.Last = &f, &l
	}
	st.Banner = raceBanner(in, st)
	return st
}

func raceBanner(in RaceInput, st raceStats) string {
	var parts []string
	n := len(in.Rows)
	if in.SuccessLabel != "" || st.Matched > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d returned the success pattern", st.Matched, n))
	}
	if len(st.Dups) > 0 {
		s := fmt.Sprintf("%d responses returned the same value", st.Dups[0].Count)
		if len(st.Dups) > 1 {
			s += fmt.Sprintf(" (%d values repeated)", len(st.Dups))
		}
		parts = append(parts, s)
	}
	if len(st.Groups) > 0 && st.Groups[0].Count >= 2 {
		g := st.Groups[0]
		if g.Hash != "" {
			parts = append(parts, fmt.Sprintf("%d identical bodies", g.Count))
		} else {
			parts = append(parts, fmt.Sprintf("%d responses with the same status and length", g.Count))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d responses recorded, %d distinct outcomes", n, len(st.Groups)))
	}
	return strings.Join(parts, "; ")
}

func fmtUs(us int64) string {
	ms := float64(us) / 1000
	switch {
	case ms >= 100:
		return strconv.FormatFloat(ms, 'f', 0, 64) + " ms"
	case ms >= 10:
		return strconv.FormatFloat(ms, 'f', 1, 64) + " ms"
	}
	return strconv.FormatFloat(ms, 'f', 2, 64) + " ms"
}

func raceNotes(in RaceInput, st raceStats) []string {
	var notes []string
	if !in.Barrier {
		notes = append(notes, "no launch barrier")
	}
	if st.HasTiming {
		notes = append(notes, fmt.Sprintf("all %d launched within %s", len(in.Rows), fmtUs(st.SpreadUs)))
	} else {
		notes = append(notes, "timing not recorded; launch spread and first/last unavailable")
	}
	return notes
}

func raceAlt(in RaceInput, st raceStats) string {
	parts := []string{
		fmt.Sprintf("Race window view of %d responses in %d outcome groups", len(in.Rows), len(st.Groups)),
		st.Banner,
	}
	parts = append(parts, strings.Join(raceNotes(in, st), "; "))
	if st.First != nil && st.Last != nil {
		parts = append(parts, fmt.Sprintf("First response #%d status %d, last response #%d status %d", st.First.Seq, st.First.Status, st.Last.Seq, st.Last.Status))
	}
	parts = append(parts, "Separate connections; not single-packet synchronisation")
	return AltFromParts(parts...)
}

// RenderIntruderRace draws the race window view from recorded data only.
func RenderIntruderRace(in RaceInput, o Opts) (Rendered, error) {
	st := analyzeRace(in)
	rows := append([]RaceRow(nil), in.Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })

	extra := raceFooterNote
	if in.RunID != "" {
		extra = "Run " + in.RunID + " - " + raceFooterNote
	}
	if !in.Barrier {
		extra += "; no launch barrier"
	}
	w := o.width()
	f := frame{
		Title:      "Race condition window view",
		Provenance: extra,
		BodyHeight: func(budget int) int { return raceLayout(w, len(rows), budget, len(st.Groups), st.HasTiming).total },
		Draw: func(c *canvas, body image.Rectangle, budget int) {
			drawRace(c, body, in, st, rows, budget)
		},
	}
	png, W, H, err := renderFrame(o, f)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{PNG: png, Alt: raceAlt(in, st), Summary: st.Banner, Kind: KindIntruderRace, Width: W, Height: H}, nil
}

type raceDims struct {
	laneH, lanes, shown, groups             int
	bannerH, launchH, lanesH, tableH, total int
}

const raceMaxGroups = 8

func raceLayout(w, n, budget, groups int, timed bool) raceDims {
	d := raceDims{bannerH: 70, launchH: 110}
	d.laneH = 16
	if n > 30 {
		d.laneH = 8
	}
	if n > 80 {
		d.laneH = 4
	}
	d.lanes = n
	if budget < d.lanes {
		d.lanes = budget
	}
	d.tableH = 40 + (min(groups, raceMaxGroups)+1)*24 + 20
	if d.tableH < 190 {
		d.tableH = 190
	}
	fixed := titleH + footerH + d.bannerH + d.launchH + 60 + d.tableH
	if avail := (maxRenderHeight - fixed) / d.laneH; d.lanes > avail {
		d.lanes = max(avail, 1)
	}
	d.lanesH = d.lanes*d.laneH + 56
	if !timed {
		d.launchH, d.lanesH = 30, 78
	}
	d.total = d.bannerH + d.launchH + d.lanesH + d.tableH
	return d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func drawRace(c *canvas, body image.Rectangle, in RaceInput, st raceStats, rows []RaceRow, budget int) {
	p := c.pal
	d := raceLayout(body.Dx(), len(rows), budget, len(st.Groups), st.HasTiming)
	x0, x1 := frameGut, body.Dx()-frameGut
	y := body.Min.Y + 10

	// banner
	c.rect(x0, y, x1-x0, d.bannerH-14, p.panel)
	c.strokeRect(x0, y, x1-x0, d.bannerH-14, p.grid)
	c.rect(x0, y, 4, d.bannerH-14, p.accent)
	c.text(x0+14, y+8, c.truncate(fontBold, 15, st.Banner, x1-x0-28), fontBold, 15, p.ink)
	c.text(x0+14, y+32, c.truncate(fontSans, 12, strings.Join(raceNotes(in, st), "  |  "), x1-x0-28), fontSans, 12, p.muted)
	y += d.bannerH

	labelW := 0
	if d.laneH >= 12 {
		labelW = 120
	}
	ax0, ax1 := x0+labelW, x1-10

	// launch window
	c.text(x0, y, "Launch window", fontBold, 13, p.ink)
	if !st.HasTiming {
		c.text(x0+130, y+1, "timing not recorded", fontSans, 12, p.muted)
		y += d.launchH
	} else {
		c.text(x0+130, y+1, fmt.Sprintf("first to last start: %s (%d requests)", fmtUs(st.SpreadUs), len(rows)), fontSans, 12, p.muted)
		span := st.SpreadUs
		if span < 1 {
			span = 1
		}
		base := y + 78
		c.rect(ax0, base, ax1-ax0, 1, p.muted)
		cols := map[int]int{}
		for _, r := range rows {
			if !rowHasTiming(r) {
				continue
			}
			x := ax0 + 6 + int(float64(r.StartUs-st.MinStartUs)/float64(span)*float64(ax1-ax0-12))
			lvl := cols[x/7]
			cols[x/7]++
			if lvl > 5 {
				lvl = 5
			}
			c.rect(x-3, base-8-lvl*8, 6, 6, p.statusColor(r.Status))
		}
		lo, hi := float64(st.MinStartUs), float64(st.MinStartUs+span)
		drawTimeAxis(c, ax0+6, ax1-6, base, lo, hi, 5)
		y += d.launchH
	}

	// response lanes
	c.text(x0, y, "Responses (start to end, shared time axis)", fontBold, 13, p.ink)
	y += 24
	laneTop := y
	if !st.HasTiming {
		c.text(x0, y+4, "Timing not recorded: lanes not drawn. Outcomes are listed below.", fontSans, 12, p.muted)
		y += d.lanesH - 24
	} else {
		maxEnd := st.MaxEndUs
		if maxEnd < 1 {
			maxEnd = 1
		}
		xs := func(us int64) int { return ax0 + int(float64(us)/float64(maxEnd)*float64(ax1-ax0)) }
		drawn := 0
		for _, r := range rows {
			if drawn >= d.lanes {
				break
			}
			ly := laneTop + drawn*d.laneH
			drawn++
			if !rowHasTiming(r) {
				continue
			}
			if d.laneH >= 12 {
				lbl := fmt.Sprintf("#%d w%d %d %s", r.Seq, r.Worker, r.Status, statusGlyph(r.Status))
				c.text(x0, ly+1, c.truncate(fontMono, 11, lbl, labelW-6), fontMono, 11, p.ink)
			}
			bx, ex := xs(r.StartUs), xs(r.EndUs)
			bw := ex - bx
			if bw < 3 {
				bw = 3
			}
			c.rect(bx, ly+1, bw, d.laneH-2, p.statusColor(r.Status))
		}
		// outline duplicated rows
		drawn = 0
		for i, r := range rows {
			if drawn >= d.lanes {
				break
			}
			ly := laneTop + drawn*d.laneH
			drawn++
			if rowHasTiming(r) && dupIndex(rows, st, i) {
				bx, ex := xs(r.StartUs), xs(r.EndUs)
				c.strokeRect(bx-1, ly, max(ex-bx, 3)+2, d.laneH, p.ink)
			}
		}
		y = laneTop + d.lanes*d.laneH + 4
		drawTimeAxis(c, ax0, ax1, y, 0, float64(maxEnd), 6)
		y += 32
		if len(rows) > d.lanes {
			c.text(x0, y-14, fmt.Sprintf("+%d more aggregated (included in outcome table)", len(rows)-d.lanes), fontSans, 12, p.muted)
		}
	}

	c.legend(x0, y, x1, []legendItem{{"2xx ok", p.success}, {"3xx", p.redirect}, {"4xx", p.client}, {"429/403/423 X", p.blocked}, {"5xx", p.server}, {"error E", p.errc}})
	c.text(x1-c.measure(fontSans, 12, "outlined = repeated extracted value"), y, "outlined = repeated extracted value", fontSans, 12, p.muted)
	y += 28
	drawRaceTable(c, x0, y, x1, in, st)
}

// dupIndex reports whether row index i (in sorted rows) carries a repeated value.
func dupIndex(rows []RaceRow, st raceStats, i int) bool {
	if len(st.Dups) == 0 {
		return false
	}
	vals := map[string]bool{}
	for _, d := range st.Dups {
		vals[d.Value] = true
	}
	for _, v := range rows[i].Extracted {
		if vals[v] {
			return true
		}
	}
	return false
}

func drawTimeAxis(c *canvas, x0, x1, y int, lo, hi float64, n int) {
	if hi <= lo {
		hi = lo + 1
	}
	for _, t := range niceTicks(lo, hi, n) {
		if t < lo-1e-9 || t > hi+1e-9 {
			continue
		}
		x := x0 + int((t-lo)/(hi-lo)*float64(x1-x0))
		c.rect(x, y, 1, 5, c.pal.muted)
		s := fmtUs(int64(t))
		lx := x - c.measure(fontSans, 11, s)/2
		if lx < 0 {
			lx = 0
		}
		c.text(lx, y+7, s, fontSans, 11, c.pal.muted)
	}
}

func drawRaceTable(c *canvas, x0, y, x1 int, in RaceInput, st raceStats) {
	p := c.pal
	tw := (x1 - x0) * 62 / 100
	colW := []int{70, 60, 70, 60, tw - 260}
	hdr := []string{"Status", "Count", "Length", "Hits", "Sample extracted value"}
	shown := min(len(st.Groups), raceMaxGroups)
	rowH := 24
	c.text(x0, y, "Outcome groups", fontBold, 13, p.ink)
	ty := y + 22
	c.rect(x0, ty, tw, rowH, p.panel)
	c.tableGrid(x0, ty, colW, rowH, shown+1)
	cx := x0
	for i, h := range hdr {
		c.text(cx+6, ty+5, h, fontBold, 12, p.ink)
		cx += colW[i]
	}
	for i := 0; i < shown; i++ {
		g := st.Groups[i]
		ry := ty + (i+1)*rowH
		cx = x0
		c.chip(cx+4, ry+2, fmt.Sprintf("%d %s", g.Status, statusGlyph(g.Status)), p.statusColor(g.Status), p.paper)
		cx += colW[0]
		c.text(cx+6, ry+5, strconv.Itoa(g.Count), fontMono, 12, p.ink)
		cx += colW[1]
		c.text(cx+6, ry+5, strconv.Itoa(g.Length), fontMono, 12, p.ink)
		cx += colW[2]
		c.text(cx+6, ry+5, strconv.Itoa(g.Matched), fontMono, 12, p.ink)
		cx += colW[3]
		s := g.Sample
		if s == "" {
			s = "-"
		}
		c.text(cx+6, ry+5, c.truncate(fontMono, 12, s, colW[4]-12), fontMono, 12, p.ink)
	}
	if len(st.Groups) > shown {
		c.text(x0, ty+(shown+1)*rowH+6, fmt.Sprintf("+%d more groups aggregated", len(st.Groups)-shown), fontSans, 12, p.muted)
	}

	// first vs last card
	cxs := x0 + tw + 20
	cw := x1 - cxs
	c.text(cxs, y, "First vs last response", fontBold, 13, p.ink)
	c.rect(cxs, ty, cw, 4*rowH+6, p.panel)
	c.strokeRect(cxs, ty, cw, 4*rowH+6, p.grid)
	if st.First == nil {
		c.text(cxs+10, ty+10, "timing not recorded", fontSans, 12, p.muted)
		return
	}
	f, l := st.First, st.Last
	col := cw / 3
	c.text(cxs+10, ty+6, "", fontSans, 12, p.muted)
	c.text(cxs+col, ty+6, "first #"+strconv.Itoa(f.Seq), fontBold, 12, p.ink)
	c.text(cxs+2*col, ty+6, "last #"+strconv.Itoa(l.Seq), fontBold, 12, p.ink)
	lines := [][3]string{
		{"ends at", fmtUs(f.EndUs), fmtUs(l.EndUs)},
		{"latency", fmtUs(f.EndUs - f.StartUs), fmtUs(l.EndUs - l.StartUs)},
		{"status", strconv.Itoa(f.Status), strconv.Itoa(l.Status)},
	}
	for i, ln := range lines {
		ry := ty + 6 + (i+1)*22
		c.text(cxs+10, ry, ln[0], fontSans, 12, p.muted)
		c.text(cxs+col, ry, ln[1], fontMono, 12, p.ink)
		c.text(cxs+2*col, ry, ln[2], fontMono, 12, p.ink)
	}
	c.text(cxs+10, ty+4*rowH+10, fmt.Sprintf("length %d vs %d; end-time gap %s", f.Length, l.Length, fmtUs(l.EndUs-f.EndUs)), fontSans, 12, p.ink)
}
