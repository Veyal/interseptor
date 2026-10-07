package preview

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestStripShadeAlphaIsLegible(t *testing.T) {
	if stripShadeAlpha[0] < 0.6 {
		t.Fatalf("lightest tint %v is too faint against white", stripShadeAlpha[0])
	}
}

func TestStripLengthHasNonColourCue(t *testing.T) {
	rows := stripFixture(20)
	rows[4].Length = 1200 + 120 // 10% off median: shade step 1
	r, err := RenderIntruderStrip(StripInput{Rows: rows}, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(r.PNG))
	rc := stripCellRect(r.Width, 4)
	plain := stripCellRect(r.Width, 5)
	// the corner tick is a darker pixel group than the plain cell's same corner
	cx, cy := rc.Max.X-3, rc.Max.Y-3
	px, py := plain.Max.X-3, plain.Max.Y-3
	a, b := img.At(cx, cy), img.At(px, py)
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	if ar == br && ag == bg && ab == bb {
		t.Fatalf("step-1 cell has no corner cue: %v vs %v", a, b)
	}
}

func TestStripPayloadHeaderSaysMasked(t *testing.T) {
	rows := stripFixture(5)
	rows[1].Status = 200
	outs := stripOutliers(StripInput{Rows: rows, Mask: true})
	if h := stripPayloadHeader(StripInput{Rows: rows, Mask: true}, outs); !strings.Contains(h, "masked") {
		t.Fatalf("header %q", h)
	}
	if h := stripPayloadHeader(StripInput{Rows: rows}, outs); strings.Contains(h, "masked") {
		t.Fatalf("header %q", h)
	}
}

func TestStripLegendSwatchesUseCellHue(t *testing.T) {
	pal := lightReportPalette()
	sw := stripLegendSwatches(pal, 401)
	if len(sw) != 3 {
		t.Fatal(len(sw))
	}
	want := blendOver(pal.statusColor(401), pal.paper, stripShadeAlpha[2])
	if sw[2] != want {
		t.Fatalf("swatch %v want status hue %v", sw[2], want)
	}
}
