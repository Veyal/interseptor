package preview

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

func wfRows() []WaterfallRow {
	return []WaterfallRow{
		{FlowID: 3, Method: "GET", Path: "/api/me", Status: 200, StartMs: 1_700_000_000_050, DurationMs: 300},
		{FlowID: 1, Method: "POST", Path: "/login", Status: 302, StartMs: 1_700_000_000_000, DurationMs: 200},
		{FlowID: 2, Method: "POST", Path: "/reset?token=secret", Status: 429, StartMs: 1_700_000_000_100, DurationMs: 250},
	}
}

func decodeWF(t *testing.T, r Rendered) (px func(x, y int) [3]uint8) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	return func(x, y int) [3]uint8 {
		r, g, b, _ := img.At(x, y).RGBA()
		return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
	}
}

func TestWaterfallOverlappingBarsShareX(t *testing.T) {
	in := WaterfallInput{Title: "Login sequence", Rows: wfRows()}
	r, err := RenderFlowWaterfall(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindFlowWaterfall || len(r.Alt) == 0 || len(r.Summary) == 0 {
		t.Fatalf("bad rendered: %+v", r)
	}
	g := waterfallGeometry(in, Opts{})
	px := decodeWF(t, r)
	pal := lightReportPalette()
	// sorted: flow1 [0,200], flow3 [50,350], flow2 [100,350]. Shared x range 100..200 ms.
	xShared := g.xAt(150)
	want := []struct {
		row    int
		status int
	}{{0, 302}, {1, 200}, {2, 429}}
	for _, w := range want {
		c := pal.statusColor(w.status)
		got := px(xShared, g.rowY(w.row)+g.rowH/2)
		if got != [3]uint8{c.R, c.G, c.B} {
			t.Errorf("row %d at x=%d: got %v want %v", w.row, xShared, got, c)
		}
	}
	// flow1 ends at 200ms: no bar at 300ms in row 0.
	if got := px(g.xAt(300), g.rowY(0)+g.rowH/2); got == [3]uint8{pal.redirect.R, pal.redirect.G, pal.redirect.B} {
		t.Errorf("row 0 bar extends past its end")
	}
}

func TestWaterfallSortStabilityAndHash(t *testing.T) {
	a := []WaterfallRow{
		{FlowID: 9, Method: "GET", Path: "/b", Status: 200, StartMs: 1000, DurationMs: 10},
		{FlowID: 4, Method: "GET", Path: "/a", Status: 200, StartMs: 1000, DurationMs: 10},
		{FlowID: 7, Method: "GET", Path: "/c", Status: 200, StartMs: 999, DurationMs: 10},
	}
	b := []WaterfallRow{a[1], a[2], a[0]}
	r1, err := RenderFlowWaterfall(WaterfallInput{Rows: a}, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := RenderFlowWaterfall(WaterfallInput{Rows: b}, Opts{})
	r3, _ := RenderFlowWaterfall(WaterfallInput{Rows: a}, Opts{})
	if sha256.Sum256(r1.PNG) != sha256.Sum256(r2.PNG) || r1.Alt != r2.Alt {
		t.Fatal("input order changed output")
	}
	if sha256.Sum256(r1.PNG) != sha256.Sum256(r3.PNG) {
		t.Fatal("hash unstable")
	}
	if i, j := strings.Index(r1.Alt, "#7"), strings.Index(r1.Alt, "#4"); i < 0 || j < 0 || i > j {
		t.Fatalf("order in alt wrong: %s", r1.Alt)
	}
	if strings.Index(r1.Alt, "#4") > strings.Index(r1.Alt, "#9") {
		t.Fatalf("equal starts must sort by FlowID: %s", r1.Alt)
	}
}

func TestWaterfallZeroDurationMinBar(t *testing.T) {
	in := WaterfallInput{Rows: []WaterfallRow{
		{FlowID: 1, Method: "GET", Path: "/x", Status: 200, StartMs: 5000, DurationMs: 0},
		{FlowID: 2, Method: "GET", Path: "/y", Status: 200, StartMs: 5000, DurationMs: 900},
	}}
	r, err := RenderFlowWaterfall(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	g := waterfallGeometry(in, Opts{})
	px := decodeWF(t, r)
	c := lightReportPalette().success
	want := [3]uint8{c.R, c.G, c.B}
	x0 := g.xAt(0)
	y := g.rowY(0) + g.rowH/2
	if px(x0, y) != want || px(x0+1, y) != want {
		t.Fatalf("0 ms flow must draw a 2px bar")
	}
}

func TestWaterfallAggregatesManyRows(t *testing.T) {
	var rows []WaterfallRow
	for i := 0; i < 150; i++ {
		rows = append(rows, WaterfallRow{FlowID: int64(i + 1), Method: "GET", Path: fmt.Sprintf("/p/%d", i), Status: 200, StartMs: int64(1000 + i*10), DurationMs: 50})
	}
	r, err := RenderFlowWaterfall(WaterfallInput{Rows: rows}, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Height > maxRenderHeight || len(r.PNG) > MaxPNGBytes {
		t.Fatalf("over caps: h=%d bytes=%d", r.Height, len(r.PNG))
	}
	if !strings.Contains(r.Alt, "more aggregated") {
		t.Fatalf("alt must mention aggregation: %s", r.Alt)
	}
	if !strings.Contains(r.Summary, "150 flows") {
		t.Fatalf("summary: %s", r.Summary)
	}
}

func TestWaterfallAxisUnitSeconds(t *testing.T) {
	short := axisUnit(4000)
	long := axisUnit(5001)
	if short.name != "ms" || long.name != "s" {
		t.Fatalf("units %q %q", short.name, long.name)
	}
}

func TestWaterfallEmptyAndHonestNote(t *testing.T) {
	r, err := RenderFlowWaterfall(WaterfallInput{}, Opts{})
	if err != nil || len(r.PNG) == 0 {
		t.Fatalf("empty: %v", err)
	}
	r, _ = RenderFlowWaterfall(WaterfallInput{Rows: wfRows()}, Opts{})
	if !strings.Contains(r.Alt, "total duration") {
		t.Fatalf("alt lacks honesty note: %s", r.Alt)
	}
}
