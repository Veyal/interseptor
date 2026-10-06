package preview

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strconv"
	"strings"
)

// ErrNoRows is returned when a renderer is given no rows to plot.
var ErrNoRows = errors.New("preview: no rows to render")

// DistRow is one recorded intruder response.
type DistRow struct {
	Seq      int
	Status   int
	Length   int
	TimeMs   int
	Flagged  bool
	Anomaly  bool
	Matched  bool
	BodyHash string
}

// DistributionInput is the plain-data input for RenderIntruderDistribution.
type DistributionInput struct {
	RunID string
	Rows  []DistRow
}

const (
	distMaxOutliers      = 10
	distRareTotal        = 20 // rare-cluster rule only applies from this many rows
	distRareCount        = 2
	distExactLengthLimit = 12
	distMaxClusterRows   = 14
	distTableRowH        = 24
	distOutlierRowH      = 22
	distChartH           = 190
	distSummaryH         = 48
	distTableW           = 470
)

// Fixed latency edges in ms; the last bucket is open-ended (5000+).
var latencyEdges = []int{0, 50, 100, 250, 500, 1000, 2500, 5000}

// Log-ish fixed length edges used when there are 12 or more distinct lengths.
var lengthEdges = []int{0, 100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000, 100000, 250000, 1000000}

func latencyBucket(ms int) int {
	b := 0
	for i, e := range latencyEdges {
		if ms >= e {
			b = i
		}
	}
	return b
}

func latencyLabel(i int) string {
	if i == len(latencyEdges)-1 {
		return "5k+"
	}
	e := latencyEdges[i]
	if e >= 1000 {
		return fmt.Sprintf("%gk", float64(e)/1000)
	}
	return strconv.Itoa(e)
}

// lengthBucketer returns a function mapping a length to its bucket key: the
// exact length when fewer than 12 distinct lengths exist, else the lower edge.
func lengthBucketer(rows []DistRow) func(int) int {
	seen := map[int]struct{}{}
	for _, r := range rows {
		seen[r.Length] = struct{}{}
		if len(seen) >= distExactLengthLimit {
			break
		}
	}
	if len(seen) < distExactLengthLimit {
		return func(l int) int { return l }
	}
	return func(l int) int {
		b := lengthEdges[0]
		for _, e := range lengthEdges {
			if l >= e {
				b = e
			}
		}
		return b
	}
}

type distCluster struct {
	Status  int
	Length  int // bucket key (exact length or lower edge)
	Exact   bool
	Count   int
	Median  int
	Example int // lowest Seq in the cluster
	Flagged int
}

type distKey struct{ status, bucket int }

func medianInt(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// buildClusters groups rows by (status, length bucket), ordered by count
// descending then status then length for determinism.
func buildClusters(rows []DistRow) []distCluster {
	bucket := lengthBucketer(rows)
	exact := true
	if len(rows) > 0 {
		exact = bucket(rows[0].Length) == rows[0].Length
		for _, r := range rows {
			if bucket(r.Length) != r.Length {
				exact = false
				break
			}
		}
	}
	times := map[distKey][]int{}
	idx := map[distKey]*distCluster{}
	var keys []distKey
	for _, r := range rows {
		k := distKey{r.Status, bucket(r.Length)}
		c := idx[k]
		if c == nil {
			c = &distCluster{Status: k.status, Length: k.bucket, Exact: exact, Example: r.Seq}
			idx[k] = c
			keys = append(keys, k)
		}
		c.Count++
		if r.Seq < c.Example {
			c.Example = r.Seq
		}
		if r.Flagged || r.Anomaly {
			c.Flagged++
		}
		times[k] = append(times[k], r.TimeMs)
	}
	out := make([]distCluster, 0, len(keys))
	for _, k := range keys {
		c := *idx[k]
		c.Median = medianInt(times[k])
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.Length < b.Length
	})
	return out
}

type distOutlier struct {
	Row    DistRow
	Reason string
}

// findOutliers lists flagged/anomaly rows, plus every row of clusters with
// count <= 2 when the run has at least 20 rows. Ordered by Seq.
func findOutliers(rows []DistRow, clusters []distCluster) []distOutlier {
	bucket := lengthBucketer(rows)
	rare := map[distKey]bool{}
	if len(rows) >= distRareTotal {
		for _, c := range clusters {
			if c.Count <= distRareCount {
				rare[distKey{c.Status, c.Length}] = true
			}
		}
	}
	var out []distOutlier
	for _, r := range rows {
		reason := ""
		switch {
		case r.Flagged:
			reason = "flagged"
		case r.Anomaly:
			reason = "anomaly"
		case rare[distKey{r.Status, bucket(r.Length)}]:
			reason = "rare"
		}
		if reason != "" {
			out = append(out, distOutlier{r, reason})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Row.Seq < out[j].Row.Seq })
	return out
}

func lengthLabel(c distCluster) string {
	if c.Exact {
		return strconv.Itoa(c.Length) + " B"
	}
	for i, e := range lengthEdges {
		if e == c.Length {
			if i == len(lengthEdges)-1 {
				return edgeLabel(e) + "+ B"
			}
			return edgeLabel(e) + "-" + edgeLabel(lengthEdges[i+1]) + " B"
		}
	}
	return strconv.Itoa(c.Length) + " B"
}

func edgeLabel(e int) string {
	switch {
	case e >= 1000000:
		return fmt.Sprintf("%gM", float64(e)/1000000)
	case e >= 1000:
		return fmt.Sprintf("%gk", float64(e)/1000)
	}
	return strconv.Itoa(e)
}

func statusLabel(status int) string {
	if status <= 0 {
		return "err"
	}
	return strconv.Itoa(status)
}

// statusClass indexes the stacked-histogram series.
func statusClass(status int) int {
	switch {
	case status <= 0 || status >= 600:
		return 5
	case isBlockedStatus(status):
		return 3
	case status >= 500:
		return 4
	case status >= 400:
		return 2
	case status >= 300:
		return 1
	}
	return 0
}

var classRepStatus = []int{200, 300, 400, 429, 500, 0}
var classNames = []string{"2xx", "3xx", "4xx", "429/403/423", "5xx", "error"}

type distSeg struct {
	N     int
	Color color.RGBA
}

type distBar struct {
	Label string
	Segs  []distSeg
}

func (b distBar) total() int {
	n := 0
	for _, s := range b.Segs {
		n += s.N
	}
	return n
}

// drawBars draws a stacked bar chart inside rect and returns the plot area.
// Bar heights are proportional to totals; the tallest bar fills the plot.
func drawBars(c *canvas, rect image.Rectangle, bars []distBar) image.Rectangle {
	plot := image.Rect(rect.Min.X+40, rect.Min.Y+26, rect.Max.X-6, rect.Max.Y-20)
	if plot.Dx() < 10 || plot.Dy() < 10 || len(bars) == 0 {
		return plot
	}
	maxN := 1
	for _, b := range bars {
		if t := b.total(); t > maxN {
			maxN = t
		}
	}
	ph := plot.Dy()
	c.rect(plot.Min.X, plot.Max.Y, plot.Dx(), 1, c.pal.muted)
	c.rect(plot.Min.X, plot.Min.Y, plot.Dx(), 1, c.pal.grid)
	c.textRight(plot.Min.X-6, plot.Min.Y-6, strconv.Itoa(maxN), fontSans, 11, c.pal.muted)
	c.textRight(plot.Min.X-6, plot.Max.Y-12, "0", fontSans, 11, c.pal.muted)
	n := len(bars)
	for i, b := range bars {
		cx := plot.Min.X + (2*i+1)*plot.Dx()/(2*n)
		slot := plot.Dx() / n
		bw := slot * 7 / 10
		if bw < 3 {
			bw = 3
		}
		x := cx - bw/2
		cum, y := 0, plot.Max.Y
		for _, s := range b.Segs {
			if s.N <= 0 {
				continue
			}
			cum += s.N
			top := plot.Max.Y - (cum*ph+maxN/2)/maxN
			if top >= y {
				top = y - 1
			}
			c.rect(x, top, bw, y-top, s.Color)
			y = top
		}
		if t := b.total(); t > 0 {
			lbl := strconv.Itoa(t)
			if c.measure(fontSans, 11, lbl) <= slot+4 {
				c.text(cx-c.measure(fontSans, 11, lbl)/2, y-14, lbl, fontSans, 11, c.pal.ink)
			}
		}
		lbl := b.Label
		if w := c.measure(fontSans, 11, lbl); w <= slot+8 {
			c.text(cx-w/2, plot.Max.Y+4, lbl, fontSans, 11, c.pal.muted)
		}
	}
	return plot
}

type distLayout struct {
	table, c1, c2, outliers image.Rectangle
	height                  int
	clusterRows             int
}

func distComputeLayout(w, nClusters, nOutlierRows int, rowsBudget int) distLayout {
	var l distLayout
	g := frameGut
	l.clusterRows = nClusters
	cap := distMaxClusterRows
	if rowsBudget < cap {
		cap = rowsBudget
	}
	if cap < 1 {
		cap = 1
	}
	if l.clusterRows > cap {
		l.clusterRows = cap
	}
	tableH := 28 + l.clusterRows*distTableRowH
	if nClusters > l.clusterRows {
		tableH += 22
	}
	top := distSummaryH
	if w >= 900 {
		l.table = image.Rect(g, top, g+distTableW, top+tableH)
		cx := g + distTableW + 24
		l.c1 = image.Rect(cx, top, w-g, top+distChartH)
		l.c2 = image.Rect(cx, top+distChartH+12, w-g, top+2*distChartH+12)
		side := 2*distChartH + 12
		if tableH > side {
			side = tableH
		}
		top += side
	} else {
		l.table = image.Rect(g, top, w-g, top+tableH)
		top += tableH + 12
		l.c1 = image.Rect(g, top, w-g, top+distChartH)
		l.c2 = image.Rect(g, top+distChartH+12, w-g, top+2*distChartH+12)
		top += 2*distChartH + 12
	}
	top += 16
	oh := 32 + nOutlierRows*distOutlierRowH
	l.outliers = image.Rect(g, top, w-g, top+oh)
	l.height = top + oh + 10
	return l
}

func distOutlierRows(n int) (shown int, more int) {
	if n > distMaxOutliers {
		return distMaxOutliers, n - distMaxOutliers
	}
	return n, 0
}

// RenderIntruderDistribution draws the response distribution for a run:
// cluster table, status counts, latency histogram and an outlier panel.
func RenderIntruderDistribution(in DistributionInput, o Opts) (Rendered, error) {
	if len(in.Rows) == 0 {
		return Rendered{}, ErrNoRows
	}
	rows := in.Rows
	clusters := buildClusters(rows)
	outliers := findOutliers(rows, clusters)
	shown, more := distOutlierRows(len(outliers))
	extraLine := 0
	if len(outliers) == 0 || more > 0 {
		extraLine = 1
	}
	title := "Intruder response distribution"
	if in.RunID != "" {
		title += " - run " + in.RunID
	}
	allTimes := make([]int, len(rows))
	for i, r := range rows {
		allTimes[i] = r.TimeMs
	}
	overall := medianInt(allTimes)
	w := o.width()

	png, W, H, err := renderFrame(o, frame{
		Title:      title,
		Provenance: fmt.Sprintf("Run %s - %d responses recorded", orDash(in.RunID), len(rows)),
		BodyHeight: func(budget int) int {
			l := distComputeLayout(w, len(clusters), shown+extraLine, budget)
			return l.height
		},
		Draw: func(c *canvas, body image.Rectangle, budget int) {
			l := distComputeLayout(w, len(clusters), shown+extraLine, budget)
			drawDistribution(c, body.Min.Y, l, rows, clusters, outliers, shown, more, overall)
		},
	})
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{PNG: png, Alt: distAlt(rows, clusters, outliers, shown, more, overall),
		Summary: distSummary(rows, clusters, outliers, overall), Kind: KindIntruderDistribution, Width: W, Height: H}, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func distSummary(rows []DistRow, cl []distCluster, out []distOutlier, med int) string {
	return fmt.Sprintf("%d responses in %d clusters, median %d ms, %d outliers", len(rows), len(cl), med, len(out))
}

func distAlt(rows []DistRow, cl []distCluster, out []distOutlier, shown, more, med int) string {
	parts := []string{fmt.Sprintf("Intruder response distribution: %d responses in %d status/length clusters, median latency %d ms", len(rows), len(cl), med)}
	var top []string
	for i, c := range cl {
		if i == 3 {
			break
		}
		top = append(top, fmt.Sprintf("%s with %s x %d", statusLabel(c.Status), lengthLabel(c), c.Count))
	}
	parts = append(parts, "Top clusters: "+strings.Join(top, "; "))
	if len(out) == 0 {
		parts = append(parts, "No flagged or rare outlier rows")
	} else {
		var o []string
		for _, x := range out[:shown] {
			o = append(o, fmt.Sprintf("#%d (%s, %s, %s)", x.Row.Seq, statusLabel(x.Row.Status), x.Row.Reasonless(), x.Reason))
		}
		s := fmt.Sprintf("%d outliers: %s", len(out), strings.Join(o, ", "))
		if more > 0 {
			s += fmt.Sprintf(", and %d more", more)
		}
		parts = append(parts, s)
	}
	return AltFromParts(parts...)
}

// Reasonless formats a row's length for alt text.
func (r DistRow) Reasonless() string { return strconv.Itoa(r.Length) + " B" }

func drawDistribution(c *canvas, y0 int, l distLayout, rows []DistRow, cl []distCluster, out []distOutlier, shown, more, med int) {
	p := c.pal
	g := frameGut
	// summary strip
	c.text(g, y0+10, fmt.Sprintf("%d responses  |  %d clusters  |  median %d ms  |  %d outliers", len(rows), len(cl), med, len(out)), fontBold, 14, p.ink)
	c.text(g, y0+28, "Cluster = (status, response length). Colour is always paired with the status label.", fontSans, 11, p.muted)
	drawClusterTable(c, y0, l, cl)
	drawStatusChart(c, y0, l.c1, rows)
	drawLatencyChart(c, y0, l.c2, rows)
	drawOutlierPanel(c, y0, l.outliers, out, shown, more)
}

func drawClusterTable(c *canvas, y0 int, l distLayout, cl []distCluster) {
	p := c.pal
	t := l.table.Add(image.Pt(0, y0))
	colW := []int{96, 120, 70, 100, 84}
	if t.Dx() < distTableW {
		colW = []int{80, 100, 60, 90, t.Dx() - 330}
		if colW[4] < 50 {
			colW[4] = 50
		}
	}
	rowH := distTableRowH
	hdr := []string{"Status", "Length", "Count", "Median ms", "Ex. seq"}
	c.rect(t.Min.X, t.Min.Y, sum(colW), 28, p.panel)
	x := t.Min.X
	for i, h := range hdr {
		c.text(x+8, t.Min.Y+7, h, fontBold, 12, p.ink)
		x += colW[i]
	}
	body := t.Min.Y + 28
	for i := 0; i < l.clusterRows; i++ {
		cc := cl[i]
		y := body + i*rowH
		col := p.statusColor(cc.Status)
		label := statusLabel(cc.Status) + " " + statusGlyph(cc.Status)
		c.chip(t.Min.X+6, y+2, label, col, p.paper)
		cells := []string{lengthLabel(cc), strconv.Itoa(cc.Count), strconv.Itoa(cc.Median), "#" + strconv.Itoa(cc.Example)}
		x := t.Min.X + colW[0]
		for j, s := range cells {
			c.text(x+8, y+5, c.truncate(fontMono, 12, s, colW[j+1]-12), fontMono, 12, p.ink)
			x += colW[j+1]
		}
	}
	end := body + l.clusterRows*rowH
	if len(cl) > l.clusterRows {
		c.text(t.Min.X+8, end+4, fmt.Sprintf("+%d more clusters", len(cl)-l.clusterRows), fontSans, 12, p.muted)
		end += 22
	}
	c.tableGrid(t.Min.X, t.Min.Y, colW, 28, 1)
	c.tableGrid(t.Min.X, body, colW, rowH, l.clusterRows)
	_ = end
}

func sum(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}

func drawStatusChart(c *canvas, y0 int, r image.Rectangle, rows []DistRow) {
	p := c.pal
	r = r.Add(image.Pt(0, y0))
	c.text(r.Min.X, r.Min.Y, "Responses per status", fontBold, 12, p.ink)
	counts := map[int]int{}
	for _, row := range rows {
		counts[row.Status]++
	}
	var st []int
	for s := range counts {
		st = append(st, s)
	}
	sort.Ints(st)
	maxBars := (r.Dx() - 46) / 44
	if maxBars < 3 {
		maxBars = 3
	}
	var bars []distBar
	other := 0
	if len(st) > maxBars {
		sort.SliceStable(st, func(i, j int) bool { return counts[st[i]] > counts[st[j]] })
		for _, s := range st[maxBars-1:] {
			other += counts[s]
		}
		st = st[:maxBars-1]
		sort.Ints(st)
	}
	for _, s := range st {
		bars = append(bars, distBar{Label: statusLabel(s) + " " + statusGlyph(s), Segs: []distSeg{{counts[s], p.statusColor(s)}}})
	}
	if other > 0 {
		bars = append(bars, distBar{Label: "other", Segs: []distSeg{{other, p.muted}}})
	}
	drawBars(c, image.Rect(r.Min.X, r.Min.Y+4, r.Max.X, r.Max.Y), bars)
}

func drawLatencyChart(c *canvas, y0 int, r image.Rectangle, rows []DistRow) {
	p := c.pal
	r = r.Add(image.Pt(0, y0))
	c.text(r.Min.X, r.Min.Y, "Latency (ms) by status class", fontBold, 12, p.ink)
	var grid [8][6]int
	used := [6]bool{}
	for _, row := range rows {
		cls := statusClass(row.Status)
		grid[latencyBucket(row.TimeMs)][cls]++
		used[cls] = true
	}
	var bars []distBar
	for b := range grid {
		bar := distBar{Label: latencyLabel(b)}
		for cls := 0; cls < 6; cls++ {
			bar.Segs = append(bar.Segs, distSeg{grid[b][cls], p.statusColor(classRepStatus[cls])})
		}
		bars = append(bars, bar)
	}
	var items []legendItem
	for cls, u := range used {
		if u {
			items = append(items, legendItem{classNames[cls] + " " + statusGlyph(classRepStatus[cls]), p.statusColor(classRepStatus[cls])})
		}
	}
	plotR := image.Rect(r.Min.X, r.Min.Y+4, r.Max.X, r.Max.Y)
	drawBars(c, plotR, bars)
	lx := r.Min.X + 40
	lw := 0
	for _, it := range items {
		lw += 34 + c.measure(fontSans, 11, it.Label)
	}
	_ = lw
	// legend right-aligned in the title row
	cx := r.Max.X
	for i := len(items) - 1; i >= 0; i-- {
		w := 16 + c.measure(fontSans, 11, items[i].Label) + 10
		cx -= w
		if cx < lx+c.measure(fontBold, 12, "Latency (ms) by status class")+8 {
			break
		}
		c.rect(cx, r.Min.Y+2, 10, 10, items[i].Color)
		c.text(cx+14, r.Min.Y, items[i].Label, fontSans, 11, p.ink)
	}
}

func drawOutlierPanel(c *canvas, y0 int, r image.Rectangle, out []distOutlier, shown, more int) {
	p := c.pal
	r = r.Add(image.Pt(0, y0))
	c.rect(r.Min.X, r.Min.Y, r.Dx(), 26, p.panel)
	c.text(r.Min.X+8, r.Min.Y+6, fmt.Sprintf("Outliers (%d): flagged or anomaly rows, and rare clusters in runs of 20+", len(out)), fontBold, 12, p.ink)
	y := r.Min.Y + 32
	if len(out) == 0 {
		c.text(r.Min.X+8, y+3, "No flagged rows or rare clusters.", fontSans, 12, p.muted)
		return
	}
	for _, o := range out[:shown] {
		row := o.Row
		c.rect(r.Min.X, y, 4, distOutlierRowH-2, p.statusColor(row.Status))
		c.text(r.Min.X+12, y+3, "#"+strconv.Itoa(row.Seq), fontMono, 12, p.ink)
		c.chip(r.Min.X+80, y, statusLabel(row.Status)+" "+statusGlyph(row.Status), p.statusColor(row.Status), p.paper)
		info := fmt.Sprintf("%d B   %d ms   %s", row.Length, row.TimeMs, o.Reason)
		if row.Matched {
			info += "   match"
		}
		if row.BodyHash != "" {
			h := row.BodyHash
			if len(h) > 8 {
				h = h[:8]
			}
			info += "   hash " + h
		}
		c.text(r.Min.X+176, y+3, c.truncate(fontMono, 12, info, r.Dx()-186), fontMono, 12, p.ink)
		y += distOutlierRowH
	}
	if more > 0 {
		c.text(r.Min.X+12, y+3, fmt.Sprintf("+%d more", more), fontBold, 12, p.muted)
	}
}
