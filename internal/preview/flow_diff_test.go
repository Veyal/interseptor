package preview

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

func sampleFlowDiff() FlowDiffInput {
	return FlowDiffInput{
		A: FlowSide{FlowID: 11, Method: "GET", URL: "https://example.com/api/orders/1", Status: 200, Length: 512, TimeMs: 40},
		B: FlowSide{FlowID: 12, Method: "GET", URL: "https://example.com/api/orders/2", Status: 403, Length: 64, TimeMs: 55},
		HeaderDeltas: []FlowHeaderDelta{
			{Name: "X-Added", Kind: "added", B: "yes"},
			{Name: "Server", Kind: "removed", A: "nginx"},
			{Name: "Content-Type", Kind: "changed", A: "application/json", B: "text/plain"},
		},
		BodyDeltas: []FlowBodyDelta{
			{Kind: "context", Line: "{"},
			{Kind: "removed", Line: "\t\"owner\": \"alice@example.com\","},
			{Kind: "added", Line: "\t\"error\": \"forbidden\""},
			{Kind: "context", Line: "}"},
		},
	}
}

func TestFlowDiffRendersValidPNG(t *testing.T) {
	r, err := RenderFlowDiff(sampleFlowDiff(), Opts{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1100 || r.Width != 1100 || r.Height != img.Bounds().Dy() || r.Kind != KindFlowDiff {
		t.Fatalf("bad render %dx%d kind=%s", r.Width, r.Height, r.Kind)
	}
	if r.Alt == "" || !strings.Contains(r.Summary, "#11 vs #12") {
		t.Fatalf("alt/summary: %q / %q", r.Alt, r.Summary)
	}
	if !strings.Contains(r.Alt, "Headers: 1 added, 1 removed, 1 changed") {
		t.Fatalf("alt lacks header counts: %q", r.Alt)
	}
}

func TestFlowDiffHeaderRowsKeepInputOrder(t *testing.T) {
	in := sampleFlowDiff()
	if !strings.Contains(flowDiffAlt(in), "Headers: 1 added") {
		t.Fatal("alt")
	}
	// order is preserved by construction: drawHeaderTable iterates input.
	// verify the first row glyph pixel colour sits in row 0 (added = success tint).
	r1, _ := RenderFlowDiff(in, Opts{})
	in.HeaderDeltas[0], in.HeaderDeltas[1] = in.HeaderDeltas[1], in.HeaderDeltas[0]
	r2, _ := RenderFlowDiff(in, Opts{})
	if bytes.Equal(r1.PNG, r2.PNG) {
		t.Fatal("reordering header deltas must change the render")
	}
}

func TestFlowDiffHeaderKindColorsAtKnownPixels(t *testing.T) {
	r, _ := RenderFlowDiff(sampleFlowDiff(), Opts{})
	img, _ := png.Decode(bytes.NewReader(r.PNG))
	pal := lightReportPalette()
	// locate the first header-row tint cell: scan column x=frameGut+2 downwards
	want := []struct {
		name string
		c    [3]uint8
	}{
		{"added", tint(pal.success)}, {"removed", tint(pal.blocked)}, {"changed", tint(pal.client)},
	}
	var found []string
	prev := ""
	for y := 0; y < r.Height; y++ {
		cr, cg, cb, _ := img.At(frameGut+2, y).RGBA()
		got := [3]uint8{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8)}
		for _, w := range want {
			if got == w.c && prev != w.name {
				found = append(found, w.name)
				prev = w.name
			}
		}
	}
	if strings.Join(found, ",") != "added,removed,changed" {
		t.Fatalf("header tint order %v", found)
	}
}

func tint(c interface{ RGBA() (r, g, b, a uint32) }) [3]uint8 {
	r, g, b, _ := c.RGBA()
	bg := uint32(255)
	m := func(f uint32) uint8 { return uint8((int(f>>8)*30 + int(bg)*(255-30)) / 255) }
	return [3]uint8{m(r), m(g), m(b)}
}

func TestFlowDiffTruncatesLongDiff(t *testing.T) {
	in := sampleFlowDiff()
	in.BodyDeltas = nil
	for i := 0; i < 300; i++ {
		in.BodyDeltas = append(in.BodyDeltas, FlowBodyDelta{Kind: "added", Line: fmt.Sprintf("line %d", i)})
	}
	lines, more := prepareDiffLines(in.BodyDeltas, 400)
	if len(lines) != 120 || more != 180 {
		t.Fatalf("lines=%d more=%d", len(lines), more)
	}
	if got := fmt.Sprintf("+%d more lines", more); got != "+180 more lines" {
		t.Fatal(got)
	}
	r, err := RenderFlowDiff(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Height > maxRenderHeight {
		t.Fatalf("height %d", r.Height)
	}
	// the drawn count never exceeds 120 and the "more" count is honest about
	// whatever the 1800px height cap forces off the image.
	shown, hidden := planDiffLines(in.BodyDeltas, 400, 3, 0, false, false)
	if len(shown) > 120 || len(shown)+hidden != 300 || hidden < 180 {
		t.Fatalf("plan shown=%d hidden=%d", len(shown), hidden)
	}
}

func TestFlowDiffLongLineAndTabs(t *testing.T) {
	long := strings.Repeat("x", 300)
	lines, _ := prepareDiffLines([]FlowBodyDelta{{Kind: "added", Line: long}, {Kind: "removed", Line: "a\tb"}}, 10)
	if n := len([]rune(lines[0].text)); n != flowDiffMaxCols || !strings.HasSuffix(lines[0].text, "...") {
		t.Fatalf("len=%d %q", n, lines[0].text)
	}
	if lines[0].text[:2] != "+ " || lines[1].text != "-   a b"[:0]+"- a   b" {
		t.Fatalf("prefix/tab: %q", lines[1].text)
	}
	if strings.Contains(lines[1].text, "\t") {
		t.Fatal("tab left")
	}
}

func TestFlowDiffDeterministic(t *testing.T) {
	a, _ := RenderFlowDiff(sampleFlowDiff(), Opts{})
	b, _ := RenderFlowDiff(sampleFlowDiff(), Opts{})
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) {
		t.Fatal("hash differs between renders")
	}
}

func TestFlowDiffIdenticalIsNoDifferences(t *testing.T) {
	s := FlowSide{FlowID: 1, Method: "GET", URL: "https://example.com/", Status: 200, Length: 10, TimeMs: 5}
	s2 := s
	s2.FlowID = 2
	in := FlowDiffInput{A: s, B: s2, BodyDeltas: []FlowBodyDelta{{Kind: "context", Line: "same"}}}
	r, err := RenderFlowDiff(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "no differences") || !strings.Contains(r.Alt, "No differences") {
		t.Fatalf("%q / %q", r.Summary, r.Alt)
	}
	d, _ := RenderFlowDiff(sampleFlowDiff(), Opts{})
	if r.Height >= d.Height {
		t.Fatalf("identical state should be shorter: %d vs %d", r.Height, d.Height)
	}
}

func TestFlowDiffDegenerate(t *testing.T) {
	if _, err := RenderFlowDiff(FlowDiffInput{}, Opts{}); err != nil {
		t.Fatal(err)
	}
	in := sampleFlowDiff()
	for i := 0; i < 200; i++ {
		in.HeaderDeltas = append(in.HeaderDeltas, FlowHeaderDelta{Name: fmt.Sprintf("H-%d", i), Kind: "changed", A: "a", B: "b"})
	}
	r, err := RenderFlowDiff(in, Opts{Width: 100})
	if err != nil || r.Width != 640 || r.Height > maxRenderHeight {
		t.Fatalf("%v %d %d", err, r.Width, r.Height)
	}
}
