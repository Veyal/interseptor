package preview

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestNiceTicks(t *testing.T) {
	cases := []struct {
		lo, hi float64
		n      int
		want   []float64
	}{
		{0, 100, 5, []float64{0, 20, 40, 60, 80, 100}},
		{0, 7, 5, []float64{0, 2, 4, 6, 8}},
		{0, 1, 4, []float64{0, 0.5, 1}},
		{13, 987, 5, []float64{0, 200, 400, 600, 800, 1000}},
		{5, 5, 4, []float64{5, 5.5, 6}},
	}
	for _, tc := range cases {
		got := niceTicks(tc.lo, tc.hi, tc.n)
		if len(got) != len(tc.want) {
			t.Fatalf("niceTicks(%v,%v,%d)=%v want %v", tc.lo, tc.hi, tc.n, got, tc.want)
		}
		for i := range got {
			if d := got[i] - tc.want[i]; d > 1e-9 || d < -1e-9 {
				t.Fatalf("niceTicks(%v,%v,%d)=%v want %v", tc.lo, tc.hi, tc.n, got, tc.want)
			}
		}
	}
}

func TestTruncateAtWidth(t *testing.T) {
	c := newCanvas(100, 40, lightReportPalette())
	defer c.close()
	s := "POST /api/v1/accounts/example/reset-password"
	full := c.measure(fontSans, 12, s)
	if got := c.truncate(fontSans, 12, s, full); got != s {
		t.Fatalf("fits but truncated: %q", got)
	}
	got := c.truncate(fontSans, 12, s, 80)
	if !strings.HasSuffix(got, "...") || c.measure(fontSans, 12, got) > 80 {
		t.Fatalf("bad truncation %q (%dpx)", got, c.measure(fontSans, 12, got))
	}
	if c.truncate(fontSans, 12, s, 0) != "" {
		t.Fatal("zero width must be empty")
	}
	for _, l := range c.wrap(fontSans, 12, strings.Repeat("abcdefghij ", 20)+strings.Repeat("x", 90), 120, 0) {
		if c.measure(fontSans, 12, l) > 120 {
			t.Fatalf("wrap line too wide: %q", l)
		}
	}
	if n := len(c.wrap(fontSans, 12, strings.Repeat("word ", 100), 100, 3)); n != 3 {
		t.Fatalf("wrap maxLines: %d", n)
	}
}

func testFrame(rows int) frame {
	return frame{
		Title: "Test render", Provenance: "run example-1",
		BodyHeight: func(r int) int { return 100 + r },
		Draw: func(c *canvas, body image.Rectangle, r int) {
			c.rect(body.Min.X+10, body.Min.Y+10, 50, 20, c.pal.success)
		},
	}
}

func TestFooterAlwaysDrawn(t *testing.T) {
	data, w, h, err := renderFrame(Opts{}, testFrame(0))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("size mismatch")
	}
	bg := lightReportPalette().panel
	ink := 0
	for y := h - footerH + 4; y < h-footerH+22; y++ {
		for x := frameGut; x < frameGut+400; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if uint8(r>>8) != bg.R || uint8(g>>8) != bg.G || uint8(b>>8) != bg.B {
				ink++
			}
		}
	}
	if ink < 200 {
		t.Fatalf("footer band looks empty (%d non-bg px)", ink)
	}
	// content that overdraws the footer cannot erase it
	f := testFrame(0)
	f.Draw = func(c *canvas, body image.Rectangle, r int) { c.fill(c.pal.paper) }
	data2, _, _, _ := renderFrame(Opts{}, f)
	img2, _ := png.Decode(bytes.NewReader(data2))
	if !reflect.DeepEqual(img.Bounds(), img2.Bounds()) {
		t.Fatal("bounds differ")
	}
	cnt := 0
	for x := frameGut; x < frameGut+400; x++ {
		if _, g, _, _ := img2.At(x, h-footerH+10).RGBA(); uint8(g>>8) != 0xff {
			cnt++
		}
	}
	if cnt == 0 {
		t.Fatal("footer erased by overdraw")
	}
}

func TestPNGDeterministic(t *testing.T) {
	a, _, _, _ := renderFrame(Opts{}, testFrame(0))
	b, _, _, _ := renderFrame(Opts{}, testFrame(0))
	if sha256.Sum256(a) != sha256.Sum256(b) {
		t.Fatal("PNG not deterministic")
	}
}

func TestDeterministicConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	sums := make([][32]byte, 8)
	for i := range sums {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, _, _, _ := renderFrame(Opts{}, testFrame(0))
			sums[i] = sha256.Sum256(d)
		}(i)
	}
	wg.Wait()
	for _, s := range sums {
		if s != sums[0] {
			t.Fatal("concurrent renders differ")
		}
	}
}

func TestSizeClamps(t *testing.T) {
	for in, want := range map[int]int{0: 1100, -5: 1100, 100: 640, 640: 640, 1000: 1000, 5000: 1600} {
		if got := (Opts{Width: in}).width(); got != want {
			t.Fatalf("width(%d)=%d want %d", in, got, want)
		}
	}
	f := testFrame(0)
	f.BodyHeight = func(int) int { return 50000 }
	_, w, h, err := renderFrame(Opts{Width: 9999}, f)
	if err != nil || w != 1600 || h != 1800 {
		t.Fatalf("w=%d h=%d err=%v", w, h, err)
	}
	if (Opts{MaxRows: 9999}).maxRows() != 400 || (Opts{MaxRows: 30}).maxRows() != 30 {
		t.Fatal("maxRows clamp")
	}
}

func TestShrinkOnPNGCap(t *testing.T) {
	calls := 0
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	_, err := encodePNG(img, 10, func() bool { calls++; return true })
	if err != errShrink || calls != 1 {
		t.Fatalf("expected shrink request, got %v calls=%d", err, calls)
	}
	if _, err := encodePNG(img, 10, func() bool { return false }); err == nil || err == errShrink {
		t.Fatalf("expected cap error, got %v", err)
	}
}

func TestStatusColourContrastAndGlyph(t *testing.T) {
	p := lightReportPalette()
	for _, st := range []int{200, 301, 404, 429, 403, 503, 0} {
		if r := contrastRatio(p.statusColor(st), p.paper); r < 4.5 {
			t.Errorf("status %d contrast %.2f < 4.5", st, r)
		}
		if statusGlyph(st) == "" {
			t.Errorf("status %d has no glyph", st)
		}
	}
	if contrastRatio(p.ink, p.paper) < 7 || contrastRatio(p.muted, p.paper) < 4.5 {
		t.Error("text colours too low contrast")
	}
	if statusGlyph(429) != "X" || statusGlyph(200) != "ok" || p.statusColor(429) != p.blocked {
		t.Error("semantic mapping")
	}
}

func TestAltFromParts(t *testing.T) {
	got := AltFromParts("  Timeline of 3  requests", "", "First block at #2.")
	if got != "Timeline of 3 requests. First block at #2." {
		t.Fatalf("%q", got)
	}
}

func TestRenderPrimitivesSmoke(t *testing.T) {
	f := testFrame(0)
	f.Draw = func(c *canvas, b image.Rectangle, r int) {
		c.statTile(b.Min.X+20, b.Min.Y+10, 200, 54, "Sent", "120", c.pal.accent)
		c.chip(b.Min.X+240, b.Min.Y+10, "429", c.pal.blocked, c.pal.paper)
		c.legend(b.Min.X+20, b.Min.Y+80, b.Max.X, []legendItem{{"2xx", c.pal.success}, {"429", c.pal.blocked}})
		c.tableGrid(b.Min.X+20, b.Min.Y+110, []int{100, 100}, 20, 2)
		c.arrow(b.Min.X+20, b.Min.Y+170, b.Min.X+200, b.Min.Y+190, c.pal.ink)
	}
	if _, _, _, err := renderFrame(Opts{Dark: true}, f); err != nil {
		t.Fatal(err)
	}
}
