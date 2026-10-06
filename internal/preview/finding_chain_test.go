package preview

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

func linearChain() ChainInput {
	return ChainInput{
		Title: "IDOR to takeover",
		Nodes: []ChainNode{
			{ID: "C", Title: "Account takeover", Severity: "critical"},
			{ID: "A", Title: "IDOR on orders", Severity: "high"},
			{ID: "B", Title: "Token leak", Severity: "medium"},
		},
		Edges: []ChainEdge{{From: "A", To: "B", Kind: "leads to"}, {From: "B", To: "C", Kind: "enables"}},
	}
}

func TestChainLinearColumnsInOrder(t *testing.T) {
	g := buildChainGraph(linearChain())
	if len(g.layers) != 3 {
		t.Fatalf("layers=%d want 3", len(g.layers))
	}
	for i, id := range []string{"A", "B", "C"} {
		if len(g.layers[i]) != 1 || g.layers[i][0].ID != id {
			t.Fatalf("layer %d = %+v want %s", i, g.layers[i], id)
		}
	}
	r, err := RenderFindingChain(linearChain(), Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindFindingChain || r.Width != 1100 || r.Height <= 0 {
		t.Fatalf("bad rendered: %+v", r)
	}
	if !strings.Contains(r.Alt, "A -> B -> C") {
		t.Fatalf("alt=%q", r.Alt)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil || img.Bounds().Dx() != r.Width || img.Bounds().Dy() != r.Height {
		t.Fatalf("decode: %v %v", err, img.Bounds())
	}
}

func TestChainHashStable(t *testing.T) {
	a, _ := RenderFindingChain(linearChain(), Opts{})
	b, _ := RenderFindingChain(linearChain(), Opts{})
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) {
		t.Fatal("not deterministic")
	}
}

func TestChainDiamond(t *testing.T) {
	in := ChainInput{
		Nodes: []ChainNode{{ID: "D", Severity: "low"}, {ID: "C"}, {ID: "B"}, {ID: "A"}},
		Edges: []ChainEdge{{"A", "B", "x"}, {"A", "C", "y"}, {"B", "D", "z"}, {"C", "D", "w"}, {"A", "D", "long"}},
	}
	g := buildChainGraph(in)
	if len(g.layers) != 3 || len(g.layers[1]) != 2 || g.layers[1][0].ID != "B" {
		t.Fatalf("layers %+v", g.layers)
	}
	if _, err := RenderFindingChain(in, Opts{}); err != nil {
		t.Fatal(err)
	}
}

func TestChainCycleNoted(t *testing.T) {
	in := ChainInput{
		Nodes: []ChainNode{{ID: "A"}, {ID: "B"}, {ID: "C"}},
		Edges: []ChainEdge{{"A", "B", "k"}, {"B", "C", "k"}, {"C", "A", "k"}, {"B", "B", "self"}},
	}
	g := buildChainGraph(in)
	if len(g.dropped) != 2 {
		t.Fatalf("dropped=%v", g.dropped)
	}
	r, err := RenderFindingChain(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Alt, "cycle") || !strings.Contains(r.Summary, "cycle") {
		t.Fatalf("cycle not noted: %q / %q", r.Alt, r.Summary)
	}
}

func TestChainSingleAndEmpty(t *testing.T) {
	r, err := RenderFindingChain(ChainInput{Nodes: []ChainNode{{ID: "A", Title: "Only", Severity: "high"}}}, Opts{})
	if err != nil || len(r.PNG) == 0 {
		t.Fatal(err)
	}
	if _, err := RenderFindingChain(ChainInput{}, Opts{}); err != nil {
		t.Fatal(err)
	}
}

func TestChainMaxNodes(t *testing.T) {
	in := ChainInput{}
	for i := 0; i < 20; i++ {
		in.Nodes = append(in.Nodes, ChainNode{ID: fmt.Sprintf("F%02d", i), Title: "t", Severity: "low"})
		if i > 0 {
			in.Edges = append(in.Edges, ChainEdge{From: fmt.Sprintf("F%02d", i-1), To: fmt.Sprintf("F%02d", i)})
		}
	}
	r, err := RenderFindingChain(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Alt, "+8 more findings") || r.Height > maxRenderHeight {
		t.Fatalf("alt=%q h=%d", r.Alt, r.Height)
	}
}
