package preview

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
)

// ChainNode is one finding in an attack-path graph.
type ChainNode struct {
	ID       string
	Title    string
	Severity string
}

// ChainEdge is a relation between two findings (From leads to To).
type ChainEdge struct {
	From, To, Kind string
}

// ChainInput is the plain-data input of RenderFindingChain.
type ChainInput struct {
	Title string
	Nodes []ChainNode
	Edges []ChainEdge
}

const (
	chainMaxNodes = 12
	chainCardW    = 220
	chainCardH    = 64
	chainRowGap   = 24
	chainMinGap   = 70
	chainMaxGap   = 160
	chainMaxNotes = 4
)

type chainGraph struct {
	layers  [][]ChainNode
	layerOf map[string]int
	edges   []ChainEdge // kept edges, sorted
	dropped []ChainEdge // cycle / self-loop edges
	hidden  int         // findings beyond chainMaxNodes
}

// buildChainGraph normalises input: stable ID order, node cap, dedup, cycle
// edges dropped, then longest-path layering with ID tie-break.
func buildChainGraph(in ChainInput) chainGraph {
	g := chainGraph{layerOf: map[string]int{}}
	seen := map[string]bool{}
	var nodes []ChainNode
	for _, n := range in.Nodes {
		if n.ID == "" || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	if len(nodes) > chainMaxNodes {
		g.hidden = len(nodes) - chainMaxNodes
		nodes = nodes[:chainMaxNodes]
	}
	keep := map[string]bool{}
	for _, n := range nodes {
		keep[n.ID] = true
	}
	type ek struct{ f, t, k string }
	dedup := map[ek]bool{}
	var edges []ChainEdge
	for _, e := range in.Edges {
		if !keep[e.From] || !keep[e.To] || dedup[ek{e.From, e.To, e.Kind}] {
			continue
		}
		dedup[ek{e.From, e.To, e.Kind}] = true
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	g.edges, g.dropped = dropCycleEdges(nodes, edges)
	g.layerOf = longestPathLayers(nodes, g.edges)
	maxL := 0
	for _, l := range g.layerOf {
		if l > maxL {
			maxL = l
		}
	}
	if len(nodes) > 0 {
		g.layers = make([][]ChainNode, maxL+1)
		for _, n := range nodes { // nodes already ID-sorted
			g.layers[g.layerOf[n.ID]] = append(g.layers[g.layerOf[n.ID]], n)
		}
	}
	return g
}

// dropCycleEdges removes self-loops and DFS back edges (visiting in ID order).
func dropCycleEdges(nodes []ChainNode, edges []ChainEdge) (kept, dropped []ChainEdge) {
	adj := map[string][]int{}
	for i, e := range edges {
		adj[e.From] = append(adj[e.From], i)
	}
	state := map[string]int{} // 0 new, 1 on stack, 2 done
	back := map[int]bool{}
	var visit func(id string)
	visit = func(id string) {
		state[id] = 1
		for _, i := range adj[id] {
			e := edges[i]
			switch state[e.To] {
			case 1:
				back[i] = true
			case 0:
				visit(e.To)
			}
		}
		state[id] = 2
	}
	for _, n := range nodes {
		if state[n.ID] == 0 {
			visit(n.ID)
		}
	}
	for i, e := range edges {
		if back[i] || e.From == e.To {
			dropped = append(dropped, e)
		} else {
			kept = append(kept, e)
		}
	}
	return kept, dropped
}

// longestPathLayers assigns each node layer = longest incoming path length.
func longestPathLayers(nodes []ChainNode, edges []ChainEdge) map[string]int {
	layer := map[string]int{}
	indeg := map[string]int{}
	out := map[string][]string{}
	for _, e := range edges {
		indeg[e.To]++
		out[e.From] = append(out[e.From], e.To)
	}
	var queue []string
	for _, n := range nodes {
		layer[n.ID] = 0
		if indeg[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, t := range out[id] {
			if layer[id]+1 > layer[t] {
				layer[t] = layer[id] + 1
			}
			if indeg[t]--; indeg[t] == 0 {
				queue = append(queue, t)
			}
		}
	}
	return layer
}

// criticalPath returns the longest chain of IDs (ties broken by ID order).
func (g chainGraph) criticalPath() []string {
	best := map[string][]string{}
	for _, layer := range g.layers {
		for _, n := range layer {
			cur := []string{n.ID}
			for _, e := range g.edges { // sorted by From; first strictly longer wins
				if e.To == n.ID && len(best[e.From])+1 > len(cur) {
					cur = append(append([]string{}, best[e.From]...), n.ID)
				}
			}
			best[n.ID] = cur
		}
	}
	var path []string
	for _, layer := range g.layers {
		for _, n := range layer {
			if len(best[n.ID]) > len(path) {
				path = best[n.ID]
			}
		}
	}
	return path
}

func (g chainGraph) nodeCount() int {
	n := 0
	for _, l := range g.layers {
		n += len(l)
	}
	return n
}

func dropNote(e ChainEdge) string {
	if e.From == e.To {
		return fmt.Sprintf("self-relation on %s", e.From)
	}
	return fmt.Sprintf("%s -> %s", e.From, e.To)
}

func (g chainGraph) summary() string {
	n := g.nodeCount()
	s := fmt.Sprintf("%d finding%s in %d stage%s, %d relation%s", n, plural(n), len(g.layers), plural(len(g.layers)), len(g.edges), plural(len(g.edges)))
	if g.hidden > 0 {
		s += fmt.Sprintf(", +%d more findings not drawn", g.hidden)
	}
	if len(g.dropped) > 0 {
		s += fmt.Sprintf(", %d cycle edge%s dropped", len(g.dropped), plural(len(g.dropped)))
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (g chainGraph) alt(title string) string {
	parts := []string{"Finding chain graph"}
	if title != "" {
		parts[0] += " titled " + title
	}
	parts = append(parts, g.summary())
	if g.nodeCount() == 0 {
		parts = append(parts, "No findings recorded")
	} else if p := g.criticalPath(); len(p) > 1 {
		parts = append(parts, "Longest path "+strings.Join(p, " -> "))
	} else {
		parts = append(parts, "No relations between findings recorded")
	}
	if len(g.dropped) > 0 {
		var d []string
		for _, e := range g.dropped {
			d = append(d, dropNote(e))
		}
		parts = append(parts, "Cycle edges dropped: "+strings.Join(d, ", "))
	}
	if g.hidden > 0 {
		parts = append(parts, fmt.Sprintf("+%d more findings", g.hidden))
	}
	return AltFromParts(parts...)
}

func severityColor(p reportPalette, sev string) color.RGBA {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "high":
		return p.blocked
	case "medium":
		return p.client
	case "low":
		return p.redirect
	}
	return p.errc
}

func severityLabel(sev string) string {
	s := strings.ToUpper(strings.TrimSpace(sev))
	if s == "" {
		return "UNRATED"
	}
	return s
}

// chainGeom is the pixel layout of the graph, independent of fonts.
type chainGeom struct {
	x0, cardW, gap, colH, headH, chanH int
	long                               int
}

func (g chainGraph) geometry(w int) chainGeom {
	n := len(g.layers)
	avail := w - 2*frameGut
	cw := chainCardW
	gap := 0
	if n > 1 {
		gap = (avail - n*cw) / (n - 1)
		if gap < chainMinGap {
			gap = chainMinGap
			cw = (avail - gap*(n-1)) / n
			if cw < 72 {
				cw = 72
				gap = (avail - n*cw) / (n - 1)
				if gap < 20 {
					gap = 20
				}
			}
		}
		if gap > chainMaxGap {
			gap = chainMaxGap
		}
	}
	total := n*cw + max(n-1, 0)*gap
	geo := chainGeom{cardW: cw, gap: gap, x0: frameGut + max(avail-total, 0)/2, headH: 34}
	rows := 1
	for _, l := range g.layers {
		rows = max(rows, len(l))
	}
	geo.colH = rows*(chainCardH+chainRowGap) - chainRowGap
	for _, e := range g.edges {
		if g.layerOf[e.To]-g.layerOf[e.From] > 1 {
			geo.long++
		}
	}
	if geo.long > 0 {
		geo.chanH = 12*geo.long + 10
	}
	return geo
}

// RenderFindingChain draws findings as a layered left-to-right attack-path graph.
func RenderFindingChain(in ChainInput, o Opts) (Rendered, error) {
	g := buildChainGraph(in)
	geo := g.geometry(o.width())
	notes := g.noteLines()
	bodyH := geo.headH + geo.chanH + geo.colH + 24 + 18*len(notes)
	if g.nodeCount() == 0 {
		bodyH = geo.headH + 60
	}
	title := "Finding chain"
	if t := strings.TrimSpace(in.Title); t != "" {
		title = "Finding chain: " + t
	}
	data, w, h, err := renderFrame(o, frame{
		Title:      title,
		Provenance: "Relations as recorded between findings; edge direction reads left to right. " + g.summary(),
		BodyHeight: func(int) int { return bodyH },
		Draw: func(c *canvas, body image.Rectangle, _ int) {
			g.draw(c, body, geo, notes)
		},
	})
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{PNG: data, Alt: g.alt(strings.TrimSpace(in.Title)), Summary: g.summary(), Kind: KindFindingChain, Width: w, Height: h}, nil
}

func (g chainGraph) noteLines() []string {
	var out []string
	if len(g.dropped) > 0 {
		for i, e := range g.dropped {
			if i == chainMaxNotes {
				out = append(out, fmt.Sprintf("+%d more cycle edges dropped", len(g.dropped)-chainMaxNotes))
				break
			}
			out = append(out, fmt.Sprintf("Cycle: relation %s (%s) dropped to keep a left-to-right order", dropNote(e), chainKindLabel(e.Kind)))
		}
	}
	if g.hidden > 0 {
		out = append(out, fmt.Sprintf("+%d more findings not drawn (showing first %d by ID)", g.hidden, chainMaxNodes))
	}
	return out
}

func chainKindLabel(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unlabelled"
	}
	return s
}

type chainPos struct{ x, y int }

func (g chainGraph) draw(c *canvas, body image.Rectangle, geo chainGeom, notes []string) {
	p := c.pal
	c.text(frameGut, body.Min.Y+10, g.summary(), fontSans, 13, p.muted)
	if g.nodeCount() == 0 {
		c.text(frameGut, body.Min.Y+headLine(geo), "No findings recorded for this chain.", fontSans, 14, p.ink)
		return
	}
	top := body.Min.Y + geo.headH + geo.chanH
	pos := map[string]chainPos{}
	for li, layer := range g.layers {
		colTop := top + (geo.colH-(len(layer)*(chainCardH+chainRowGap)-chainRowGap))/2
		for ri, n := range layer {
			pos[n.ID] = chainPos{geo.x0 + li*(geo.cardW+geo.gap), colTop + ri*(chainCardH+chainRowGap)}
		}
	}
	g.drawEdges(c, body, geo, pos)
	for li, layer := range g.layers {
		_ = li
		for _, n := range layer {
			g.drawCard(c, geo, pos[n.ID], n)
		}
	}
	y := top + geo.colH + 20
	for _, s := range notes {
		c.text(frameGut, y, c.truncate(fontSans, 12, s, body.Dx()-2*frameGut), fontSans, 12, p.muted)
		y += 18
	}
}

func headLine(chainGeom) int { return 40 }

func (g chainGraph) drawCard(c *canvas, geo chainGeom, at chainPos, n ChainNode) {
	p := c.pal
	sc := severityColor(p, n.Severity)
	c.rect(at.x, at.y, geo.cardW, chainCardH, p.panel)
	c.strokeRect(at.x, at.y, geo.cardW, chainCardH, p.muted)
	c.rect(at.x, at.y, 5, chainCardH, sc)
	cw := c.chip(at.x+12, at.y+6, c.truncate(fontBold, 12, sevText(n.Severity, geo.cardW), geo.cardW-40), sc, p.paper)
	if room := geo.cardW - 12 - cw - 12 - 12; room > 24 {
		c.textRight(at.x+geo.cardW-8, at.y+9, c.truncate(fontMono, 11, n.ID, room), fontMono, 11, p.muted)
	}
	title := n.Title
	if strings.TrimSpace(title) == "" {
		title = n.ID
	}
	for i, ln := range c.wrap(fontSans, 12, title, geo.cardW-24, 2) {
		c.text(at.x+12, at.y+31+i*15, ln, fontSans, 12, p.ink)
	}
}

// port spreads n attachment points along a card side.
func port(at chainPos, idx, n int) int {
	if n <= 1 {
		return at.y + chainCardH/2
	}
	step := (chainCardH - 24) / (n - 1)
	return at.y + 12 + idx*step
}

func (g chainGraph) drawEdges(c *canvas, body image.Rectangle, geo chainGeom, pos map[string]chainPos) {
	p := c.pal
	outN, inN := map[string]int{}, map[string]int{}
	for _, e := range g.edges {
		outN[e.From]++
		inN[e.To]++
	}
	// order incoming/outgoing ports by the other end's vertical position
	outIdx, inIdx := map[int]int{}, map[int]int{}
	for id := range pos {
		var outs, ins []int
		for i, e := range g.edges {
			if e.From == id {
				outs = append(outs, i)
			}
			if e.To == id {
				ins = append(ins, i)
			}
		}
		sort.SliceStable(outs, func(a, b int) bool { return pos[g.edges[outs[a]].To].y < pos[g.edges[outs[b]].To].y })
		sort.SliceStable(ins, func(a, b int) bool { return pos[g.edges[ins[a]].From].y < pos[g.edges[ins[b]].From].y })
		for k, i := range outs {
			outIdx[i] = k
		}
		for k, i := range ins {
			inIdx[i] = k
		}
	}
	chanTop := body.Min.Y + geo.headH
	long := 0
	for i, e := range g.edges {
		s, t := pos[e.From], pos[e.To]
		sy := port(s, outIdx[i], outN[e.From])
		ty := port(t, inIdx[i], inN[e.To])
		sx := s.x + geo.cardW
		tx := t.x
		span := g.layerOf[e.To] - g.layerOf[e.From]
		label := strings.TrimSpace(e.Kind)
		if span <= 1 {
			xv := sx + 14 + (outIdx[i]%5)*6
			c.line(sx, sy, xv, sy, p.muted)
			c.line(xv, sy, xv, ty, p.muted)
			c.arrow(xv, ty, tx, ty, p.muted)
			g.edgeLabel(c, label, xv+4, tx-4, ty, true)
			continue
		}
		cy := chanTop + 6 + long*12
		long++
		x1 := sx + 8 + (outIdx[i]%4)*4
		x2 := tx - 10 - (inIdx[i]%3)*4
		c.line(sx, sy, x1, sy, p.muted)
		c.line(x1, sy, x1, cy, p.muted)
		c.line(x1, cy, x2, cy, p.muted)
		c.line(x2, cy, x2, ty, p.muted)
		c.arrow(x2, ty, tx, ty, p.muted)
		g.edgeLabel(c, label, x2-200, x2-6, cy, false)
	}
}

// edgeLabel draws a label on a paper background; right-aligned to xr, above
// the line (above=true) or sitting on the line (above=false).
func (g chainGraph) edgeLabel(c *canvas, label string, xl, xr, y int, above bool) {
	if label == "" || xr-xl < 28 {
		return
	}
	s := c.truncate(fontSans, 11, label, xr-xl)
	if s == "" {
		return
	}
	w := c.measure(fontSans, 11, s)
	ly := y - 14
	if !above {
		ly = y - 7
	}
	c.rect(xr-w-2, ly, w+4, 13, c.pal.paper)
	c.text(xr-w, ly, s, fontSans, 11, c.pal.ink)
}

// sevText abbreviates the severity label on narrow cards so it stays whole.
func sevText(sev string, cardW int) string {
	l := severityLabel(sev)
	if cardW >= 130 {
		return l
	}
	switch l {
	case "CRITICAL":
		return "CRIT"
	case "MEDIUM":
		return "MED"
	case "UNRATED":
		return "N/A"
	}
	return l
}
