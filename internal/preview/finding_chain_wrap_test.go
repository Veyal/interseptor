package preview

import (
	"fmt"
	"strings"
	"testing"
)

func longChain(n int) ChainInput {
	in := ChainInput{Title: "Long chain"}
	for i := 0; i < n; i++ {
		in.Nodes = append(in.Nodes, ChainNode{ID: fmt.Sprintf("F%02d", i+1), Title: "A fairly long finding title that needs space", Severity: "high"})
		if i > 0 {
			in.Edges = append(in.Edges, ChainEdge{From: fmt.Sprintf("F%02d", i), To: fmt.Sprintf("F%02d", i+1), Kind: "leads to"})
		}
	}
	return in
}

func TestChainWrapsLongChainIntoRows(t *testing.T) {
	g := buildChainGraph(longChain(12))
	geo := g.geometry(1100)
	if geo.rows < 2 || geo.perRow >= 12 {
		t.Fatalf("12 stages must wrap: rows=%d perRow=%d", geo.rows, geo.perRow)
	}
	if geo.cardW < 140 {
		t.Fatalf("cards too narrow to read: %d", geo.cardW)
	}
	pos := g.positions(geo)
	a, b := pos["F05"], pos["F07"]
	if b.y <= a.y {
		t.Fatalf("later stage must sit on a later row: %+v %+v", a, b)
	}
	if pos["F06"].x != pos["F01"].x {
		t.Fatalf("row 2 starts at the left edge: %+v vs %+v", pos["F06"], pos["F01"])
	}
	r, err := RenderFindingChain(longChain(12), Opts{})
	if err != nil || r.Height > maxRenderHeight {
		t.Fatalf("err=%v h=%d", err, r.Height)
	}
	short := buildChainGraph(linearChain()).geometry(1100)
	if short.rows != 1 {
		t.Fatalf("short chain must stay one row, rows=%d", short.rows)
	}
}

func TestChainNarrowWidthWrapsEarlier(t *testing.T) {
	g := buildChainGraph(longChain(6))
	if geo := g.geometry(640); geo.rows < 2 || geo.cardW < 140 {
		t.Fatalf("640px: rows=%d cardW=%d", geo.rows, geo.cardW)
	}
}

func TestChainTitleGetsReadableLines(t *testing.T) {
	g := buildChainGraph(longChain(12))
	geo := g.geometry(1100)
	c := newCanvas(1100, 200, lightReportPalette())
	defer c.close()
	lines := c.wrap(fontSans, 12, "A fairly long finding title that needs space", geo.cardW-24, geo.titleLines)
	if len(lines) < 2 || strings.HasSuffix(lines[0], "...") {
		t.Fatalf("lines=%v", lines)
	}
}

func TestChainHiddenNoteAppearsOnce(t *testing.T) {
	g := buildChainGraph(longChain(16))
	if strings.Contains(g.provenance(), "not drawn") || strings.Contains(g.headline(), "not drawn") {
		t.Fatalf("hidden note must only appear in the notes block: %q / %q", g.provenance(), g.headline())
	}
	if !strings.Contains(strings.Join(g.noteLines(), " "), "+4 more findings not drawn") {
		t.Fatal("notes lost the hidden-findings line")
	}
	r, _ := RenderFindingChain(longChain(16), Opts{})
	if !strings.Contains(r.Summary, "+4 more findings not drawn") {
		t.Fatalf("summary keeps it for captions: %s", r.Summary)
	}
}
