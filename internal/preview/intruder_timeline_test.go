package preview

import (
	"bytes"
	"crypto/sha256"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tlFixture12() TimelineInput {
	in := TimelineInput{RunID: "run-example-1", Attack: "repeat", Threads: 2, DelayMs: 50, Target: "api.example.com"}
	for i := 1; i <= 12; i++ {
		st := 200
		if i > 10 {
			st = 429
		}
		start := int64(i-1) * 50000
		r := TimelineRow{Seq: i, Worker: (i-1)%2 + 1, StartUs: start, EndUs: start + 30000, Status: st, Length: 512}
		if st == 429 {
			r.RLHeaders = map[string]string{"Retry-After": "30"}
		}
		in.Rows = append(in.Rows, r)
	}
	return in
}

func TestTimelineMarkerAndSummary(t *testing.T) {
	in := tlFixture12()
	r, err := RenderIntruderTimeline(in, Opts{Width: 640})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "10 of 12 returned 2xx") || r.Kind != KindIntruderTimeline {
		t.Fatalf("summary/kind: %q %q", r.Summary, r.Kind)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	bySeq, st := tlPrepare(in)
	if st.firstBlock == nil || st.firstBlock.Seq != 11 {
		t.Fatalf("first block: %+v", st.firstBlock)
	}
	if st.burstAfterSeq != 0 {
		t.Fatalf("no burst expected, got %d", st.burstAfterSeq)
	}
	g := timelineGeometry(in, bySeq, st, Opts{Width: 640}, defaultMaxRows)
	x := g.XOf(st.firstBlock.EndUs)
	y := titleH + g.PlotTop - 2
	got := img.At(x, y)
	wr, wg, wb, _ := lightReportPalette().blocked.RGBA()
	gr, gg, gb, _ := got.RGBA()
	if wr != gr || wg != gg || wb != gb {
		t.Fatalf("pixel at (%d,%d) = %v, want blocked red", x, y, got)
	}
	if !strings.Contains(r.Alt, "request #11") || !strings.Contains(r.Alt, "p95") {
		t.Fatalf("alt: %s", r.Alt)
	}
}

func TestTimelineDeterministicAndGolden(t *testing.T) {
	in := tlFixture12()
	o := Opts{Width: 640}
	a, _ := RenderIntruderTimeline(in, o)
	b, _ := RenderIntruderTimeline(in, o)
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) || a.Alt != b.Alt {
		t.Fatal("renders differ")
	}
	compareGolden(t, "intruder_timeline", a.PNG)
}

func TestTimelineNoTimingFallback(t *testing.T) {
	in := tlFixture12()
	for i := range in.Rows {
		in.Rows[i].StartUs, in.Rows[i].EndUs = 0, 0
	}
	r, err := RenderIntruderTimeline(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Alt, "not recorded") {
		t.Fatalf("alt: %s", r.Alt)
	}
	if !strings.Contains(r.Summary, "10 of 12") {
		t.Fatal(r.Summary)
	}
}

func TestTimelineBurstAndCapped(t *testing.T) {
	in := TimelineInput{RunID: "r", Attack: "sniper", Threads: 1, Capped: true}
	for i := 1; i <= 20; i++ {
		st := 200
		if i > 10 {
			st = 429
		}
		in.Rows = append(in.Rows, TimelineRow{Seq: i, Worker: 1, StartUs: int64(i) * 1000, EndUs: int64(i)*1000 + 500, Status: st})
	}
	_, st := tlPrepare(in)
	if st.burstAfterSeq != 7 { // rows 8..12 hold 3 blocked (11,12 ... ) -> window after #7? verify
		// window after seq s covers s+1..s+5; need >=3 blocked: s=8 covers 9..13 -> 11,12,13 = 3
		if st.burstAfterSeq != 8 {
			t.Fatalf("burst after %d", st.burstAfterSeq)
		}
	}
	r, err := RenderIntruderTimeline(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "(capped at 2000)") || !strings.Contains(r.Alt, "burst") && !strings.Contains(r.Alt, "Throttling") {
		t.Fatalf("%q / %q", r.Summary, r.Alt)
	}
}

func TestTimelineManyRowsBounded(t *testing.T) {
	in := TimelineInput{RunID: "big", Attack: "repeat", Threads: 16}
	for i := 1; i <= 1500; i++ {
		st := 200
		if i > 900 {
			st = 429
		}
		in.Rows = append(in.Rows, TimelineRow{Seq: i, Worker: i%16 + 1, StartUs: int64(i) * 700, EndUs: int64(i)*700 + 9000, Status: st})
	}
	r, err := RenderIntruderTimeline(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Height > 1800 || len(r.PNG) > MaxPNGBytes {
		t.Fatalf("h=%d png=%d", r.Height, len(r.PNG))
	}
	if !strings.Contains(r.Summary, "of 1500") {
		t.Fatal(r.Summary)
	}
	for _, n := range []int{0, 1} {
		in2 := in
		in2.Rows = in.Rows[:n]
		if _, err := RenderIntruderTimeline(in2, Opts{}); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}

func TestTimelineFooterProvenance(t *testing.T) {
	r, _ := RenderIntruderTimeline(tlFixture12(), Opts{Width: 640})
	img, _ := png.Decode(bytes.NewReader(r.PNG))
	// footer band must contain ink-coloured text pixels
	ink := lightReportPalette().ink
	found := false
	for y := r.Height - footerH; y < r.Height && !found; y++ {
		for x := 0; x < r.Width; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			ir, ig, ib, _ := ink.RGBA()
			if cr == ir && cg == ig && cb == ib {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no footer text pixels")
	}
}

func TestTimelineSamples(t *testing.T) {
	dir := os.Getenv("TIMELINE_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set TIMELINE_SAMPLE_DIR")
	}
	big := TimelineInput{RunID: "run-big", Attack: "repeat", Threads: 10, DelayMs: 20, Target: "api.example.com"}
	for i := 1; i <= 300; i++ {
		st := 200
		if i > 180 {
			st = 429
		}
		if i%97 == 0 {
			st = 500
		}
		s := int64(i) * 20000
		big.Rows = append(big.Rows, TimelineRow{Seq: i, Worker: (i-1)%10 + 1, StartUs: s, EndUs: s + 40000 + int64(i%7)*9000, Status: st,
			RLHeaders: map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "60"}})
	}
	nt := tlFixture12()
	for i := range nt.Rows {
		nt.Rows[i].StartUs, nt.Rows[i].EndUs = 0, 0
	}
	for name, in := range map[string]TimelineInput{"small": tlFixture12(), "big": big, "notiming": nt} {
		r, err := RenderIntruderTimeline(in, Opts{})
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, name+".png"), r.PNG, 0o644)
	}
}
