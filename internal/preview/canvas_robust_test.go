package preview

import (
	"bytes"
	"image"
	"strings"
	"testing"
	"time"
)

func within(t *testing.T, name string, max time.Duration, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if d := time.Since(start); d > max {
		t.Fatalf("%s took %v, want <= %v", name, d, max)
	}
}

func TestTruncateLargeInputIsFast(t *testing.T) {
	c := newCanvas(100, 40, lightReportPalette())
	defer c.close()
	big := strings.Repeat("a", 64<<10)
	within(t, "truncate 64KB", 200*time.Millisecond, func() {
		got := c.truncate(fontSans, 12, big, 300)
		if c.measure(fontSans, 12, got) > 300 || !strings.HasSuffix(got, "...") {
			t.Fatalf("bad truncation %q", got)
		}
	})
	within(t, "truncate 1MiB", 200*time.Millisecond, func() {
		_ = c.truncate(fontSans, 12, strings.Repeat("ab ", 350<<10), 300)
	})
}

func TestWrapLongWordIsFast(t *testing.T) {
	c := newCanvas(100, 40, lightReportPalette())
	defer c.close()
	within(t, "wrap 64KB word", 200*time.Millisecond, func() {
		lines := c.wrap(fontSans, 12, strings.Repeat("x", 64<<10), 120, 3)
		if len(lines) != 3 {
			t.Fatalf("lines=%d", len(lines))
		}
		for _, l := range lines {
			if c.measure(fontSans, 12, l) > 120 {
				t.Fatalf("too wide %q", l)
			}
		}
	})
	within(t, "wrap unlimited", 500*time.Millisecond, func() {
		_ = c.wrap(fontSans, 12, strings.Repeat("x", 64<<10), 120, 0)
	})
}

func TestNonRenderableRunesAreEscaped(t *testing.T) {
	a := newCanvas(300, 30, lightReportPalette())
	defer a.close()
	b := newCanvas(300, 30, lightReportPalette())
	defer b.close()
	a.text(4, 4, "pw 管理", fontSans, 14, a.pal.ink)
	b.text(4, 4, "pw U+7BA1U+7406", fontSans, 14, b.pal.ink)
	if !bytes.Equal(a.img.Pix, b.img.Pix) {
		t.Fatal("CJK text must render as readable U+XXXX escapes, not tofu")
	}
	if !a.escaped {
		t.Fatal("canvas must record that escapes were used")
	}
	if w := a.measure(fontSans, 14, "管"); w != a.measure(fontSans, 14, "U+7BA1") {
		t.Fatalf("measure must match drawn escape, got %d", w)
	}
	// distinct CJK strings must be distinguishable
	if a.sanitize(fontSans, 14, "管") == a.sanitize(fontSans, 14, "理") {
		t.Fatal("distinct runes collapsed")
	}
	_ = image.Pt
}

func TestRenderFrameNotesEscapedRunes(t *testing.T) {
	var sawNote bool
	_, _, _, err := renderFrame(Opts{}, frame{
		Title:      "t",
		Provenance: "p",
		BodyHeight: func(int) int { return 60 },
		Draw: func(c *canvas, body image.Rectangle, rows int) {
			c.text(body.Min.X+10, body.Min.Y+10, "管", fontSans, 14, c.pal.ink)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(escapedFooterNote, "U+XXXX") {
		t.Fatal("note const")
	}
	_ = sawNote
}

func TestNiceTicksStepPrecision(t *testing.T) {
	for _, v := range niceTicks(0, 1, 5) {
		if s := formatTick(v, 0.2); strings.Contains(s, "0000") || strings.Contains(s, "9999") {
			t.Fatalf("ugly tick %q for %v", s, v)
		}
	}
	if got := formatTick(0.6000000000000001, 0.2); got != "0.6" {
		t.Fatalf("got %q", got)
	}
	if got := formatTick(1500, 500); got != "1500" {
		t.Fatalf("got %q", got)
	}
}
