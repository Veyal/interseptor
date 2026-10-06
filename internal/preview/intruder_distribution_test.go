package preview

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func distFixture() DistributionInput {
	in := DistributionInput{RunID: "run-example"}
	seq := 0
	add := func(n, status, length, ms int, flagged bool) {
		for i := 0; i < n; i++ {
			seq++
			in.Rows = append(in.Rows, DistRow{Seq: seq, Status: status, Length: length, TimeMs: ms + i, Flagged: flagged && i == 0})
		}
	}
	add(10, 401, 120, 40, false)  // median of 40..49 = 44
	add(5, 200, 5000, 300, false) // 300..304 median 302
	add(2, 423, 90, 900, true)    // late lockout cluster, first row flagged
	add(1, 500, 70, 2600, false)
	add(4, 200, 5000, 6000, false)
	return in
}

func TestDistributionClustersAndMedian(t *testing.T) {
	cl := buildClusters(distFixture().Rows)
	byKey := map[string]distCluster{}
	for _, c := range cl {
		byKey[fmt.Sprintf("%d/%d", c.Status, c.Length)] = c
	}
	if c := byKey["401/120"]; c.Count != 10 || c.Median != 44 || c.Example != 1 {
		t.Fatalf("401 cluster: %+v", c)
	}
	if c := byKey["200/5000"]; c.Count != 9 || c.Median != 304 {
		t.Fatalf("200 cluster: %+v", c) // 300..304,6000..6003 sorted -> median 304
	}
	if cl[0].Status != 401 {
		t.Fatalf("not sorted by count: %+v", cl[0])
	}
	if medianInt([]int{1, 2, 3, 10}) != 2 || medianInt([]int{7}) != 7 || medianInt(nil) != 0 {
		t.Fatal("medianInt")
	}
}

func TestDistributionOutliers(t *testing.T) {
	rows := distFixture().Rows // total 22 >= 20, so clusters <=2 are rare
	cl := buildClusters(rows)
	out := findOutliers(rows, cl)
	seqs := map[int]string{}
	for _, o := range out {
		seqs[o.Row.Seq] = o.Reason
	}
	if seqs[16] != "flagged" { // first 423 row
		t.Fatalf("flagged missing: %v", seqs)
	}
	if _, ok := seqs[17]; !ok {
		t.Fatalf("rare 423 row missing: %v", seqs)
	}
	if _, ok := seqs[18]; !ok {
		t.Fatalf("rare 500 row missing: %v", seqs)
	}
	if _, ok := seqs[1]; ok {
		t.Fatal("common row listed")
	}
	// below 20 rows, rarity does not apply
	small := rows[:15]
	if got := findOutliers(small, buildClusters(small)); len(got) != 0 {
		t.Fatalf("small run outliers: %v", got)
	}
}

func TestDistributionEmpty(t *testing.T) {
	_, err := RenderIntruderDistribution(DistributionInput{RunID: "x"}, Opts{})
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("err=%v", err)
	}
}

func TestDistributionRenderStable(t *testing.T) {
	a, err := RenderIntruderDistribution(distFixture(), Opts{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RenderIntruderDistribution(distFixture(), Opts{})
	if !bytes.Equal(a.PNG, b.PNG) {
		t.Fatal("not deterministic")
	}
	if a.Kind != KindIntruderDistribution || a.Width != 1100 || a.Alt == "" || a.Summary == "" {
		t.Fatalf("meta: %+v", a)
	}
	img, err := png.Decode(bytes.NewReader(a.PNG))
	if err != nil || img.Bounds().Dx() != 1100 || img.Bounds().Dy() != a.Height {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(a.Alt, "401") || !strings.Contains(a.Alt, "#16") {
		t.Fatalf("alt: %s", a.Alt)
	}
	d, _ := RenderIntruderDistribution(DistributionInput{RunID: "other", Rows: distFixture().Rows}, Opts{})
	if bytes.Equal(a.PNG, d.PNG) {
		t.Fatal("run id not reflected")
	}
}

func TestDistributionOutlierCap(t *testing.T) {
	in := DistributionInput{RunID: "r"}
	for i := 1; i <= 30; i++ {
		in.Rows = append(in.Rows, DistRow{Seq: i, Status: 200, Length: 10, TimeMs: 5, Flagged: true})
	}
	rows := in.Rows
	out := findOutliers(rows, buildClusters(rows))
	if len(out) != 30 {
		t.Fatalf("outliers %d", len(out))
	}
	r, err := RenderIntruderDistribution(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Alt, "20 more") {
		t.Fatalf("alt lacks +N more: %s", r.Alt)
	}
}

func TestDistributionBarHeightsProportional(t *testing.T) {
	pal := lightReportPalette()
	c := newCanvas(300, 200, pal)
	defer c.close()
	bars := []distBar{
		{Label: "a", Segs: []distSeg{{10, pal.success}}},
		{Label: "b", Segs: []distSeg{{5, pal.blocked}}},
		{Label: "c", Segs: []distSeg{{2, pal.server}, {3, pal.client}}},
	}
	plot := drawBars(c, image.Rect(0, 0, 300, 200), bars)
	count := func(i int, col [4]uint8) int {
		x := plot.Min.X + (2*i+1)*plot.Dx()/(2*len(bars))
		n := 0
		for y := plot.Min.Y; y <= plot.Max.Y; y++ {
			p := c.img.RGBAAt(x, y)
			if [4]uint8{p.R, p.G, p.B, p.A} == col {
				n++
			}
		}
		return n
	}
	rgba := func(r, g, b uint8) [4]uint8 { return [4]uint8{r, g, b, 255} }
	a := count(0, rgba(pal.success.R, pal.success.G, pal.success.B))
	b := count(1, rgba(pal.blocked.R, pal.blocked.G, pal.blocked.B))
	s1 := count(2, rgba(pal.server.R, pal.server.G, pal.server.B))
	s2 := count(2, rgba(pal.client.R, pal.client.G, pal.client.B))
	if a < 20 || abs(a-2*b) > 2 {
		t.Fatalf("heights a=%d b=%d", a, b)
	}
	if abs(s1+s2-b) > 2 || abs(3*s1-2*s2) > 4 {
		t.Fatalf("stack s1=%d s2=%d b=%d", s1, s2, b)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestLatencyBucket(t *testing.T) {
	cases := map[int]int{-5: 0, 0: 0, 49: 0, 50: 1, 99: 1, 100: 2, 249: 2, 250: 3, 999: 4, 1000: 5, 2499: 5, 2500: 6, 4999: 6, 5000: 7, 90000: 7}
	for ms, want := range cases {
		if got := latencyBucket(ms); got != want {
			t.Fatalf("latencyBucket(%d)=%d want %d", ms, got, want)
		}
	}
}

func TestLengthBucketing(t *testing.T) {
	few := []DistRow{{Length: 12}, {Length: 99}}
	if lengthBucketer(few)(99) != 99 {
		t.Fatal("exact when few distinct")
	}
	var many []DistRow
	for i := 0; i < 15; i++ {
		many = append(many, DistRow{Length: 1000 + i*7})
	}
	f := lengthBucketer(many)
	if f(1000) != f(1050) || f(1000) != 1000 {
		t.Fatalf("log bucket: %d %d", f(1000), f(1050))
	}
}

func TestDistributionLargeRunRenders(t *testing.T) {
	in := DistributionInput{RunID: "big"}
	for i := 1; i <= 10000; i++ {
		in.Rows = append(in.Rows, DistRow{Seq: i, Status: 200 + (i%7)*50, Length: i * 3 % 4000, TimeMs: i % 6000, Anomaly: i%997 == 0})
	}
	r, err := RenderIntruderDistribution(in, Opts{})
	if err != nil || r.Height > maxRenderHeight || len(r.PNG) > MaxPNGBytes {
		t.Fatalf("big: %v h=%d", err, r.Height)
	}
}
