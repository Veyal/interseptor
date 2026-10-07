package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func decodeRender(t *testing.T, r Rendered) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func sameRGB(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

// raceLaneBase is the y of the first lane for a timed run (body starts under
// the title bar).
func raceLaneBase(d raceDims) int {
	y := titleH + 10 + d.bannerH + d.launchH + 24
	if d.sampled {
		y += 34
	}
	return y
}

func TestRaceLaneBarsHaveStatusColourAtComputedCoordinates(t *testing.T) {
	in := raceFixture()
	r, err := RenderIntruderRace(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	img := decodeRender(t, r)
	st := analyzeRace(in)
	d := raceLayout(r.Width, len(in.Rows), defaultMaxRows, len(st.Groups), st.HasTiming)
	pal := lightReportPalette()
	ax0, ax1 := frameGut+150, r.Width-frameGut-10
	xs := func(us int64) int { return ax0 + int(float64(us)/float64(st.MaxEndUs)*float64(ax1-ax0)) }
	top := raceLaneBase(d)
	for i, row := range in.Rows {
		x := xs((row.StartUs + row.EndUs) / 2)
		y := top + i*d.laneH + d.laneH/2
		if got := img.At(x, y); !sameRGB(got, pal.statusColor(row.Status)) && !sameRGB(got, pal.ink) {
			t.Fatalf("lane %d (status %d) at (%d,%d): got %v", i, row.Status, x, y, got)
		}
		// before the bar starts, the lane is background, not a bar
		if row.StartUs > 3000 {
			if got := img.At(xs(row.StartUs)-6, y); sameRGB(got, pal.statusColor(row.Status)) {
				t.Fatalf("lane %d paints before its start", i)
			}
		}
	}
	// the last two rows are 409s and must differ in colour from the 200 lanes
	if sameRGB(pal.statusColor(200), pal.statusColor(409)) {
		t.Fatal("palette collision")
	}
}

func TestChainCardsAndEdgesAreDrawn(t *testing.T) {
	in := linearChain()
	r, err := RenderFindingChain(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	img := decodeRender(t, r)
	g := buildChainGraph(in)
	geo := g.geometry(r.Width)
	pos := g.positions(geo)
	pal := lightReportPalette()
	for _, n := range in.Nodes {
		at := pos[n.ID]
		at.y += titleH
		// severity stripe on the card's left edge
		if got := img.At(at.x+2, at.y+geo.cardH/2); !sameRGB(got, severityColor(pal, n.Severity)) {
			t.Fatalf("card %s stripe: got %v want %v", n.ID, got, severityColor(pal, n.Severity))
		}
		// card outline
		if got := img.At(at.x+geo.cardW/2, at.y); !sameRGB(got, pal.muted) {
			t.Fatalf("card %s outline: got %v", n.ID, got)
		}
	}
	// A -> B: a line leaves A's right edge at mid height
	a := pos["A"]
	if got := img.At(a.x+geo.cardW+4, a.y+titleH+geo.cardH/2); !sameRGB(got, pal.muted) {
		t.Fatalf("edge from A missing: %v", got)
	}
	// and the gap above the cards stays background
	if got := img.At(a.x+geo.cardW+4, a.y+titleH-6); !sameRGB(got, pal.paper) {
		t.Fatalf("unexpected ink above the edge: %v", got)
	}
}
