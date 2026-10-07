package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Golden comparison tolerates small anti-aliasing drift (font or image library
// upgrades) but still catches layout and colour regressions: every pixel of a
// 2x grayscale thumbnail may differ by up to goldenLumaTol, and at most
// goldenMaxDiffPct percent of pixels may exceed that. UPDATE_GOLDEN=1
// regenerates the files.
const (
	goldenLumaTol    = 24
	goldenMaxDiffPct = 0.5
	goldenMaxBytes   = 20 * 1024
)

func goldenThumb(t *testing.T, data []byte) *image.Gray {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	th := image.NewGray(image.Rect(0, 0, b.Dx()/2, b.Dy()/2))
	for y := 0; y < th.Rect.Dy(); y++ {
		for x := 0; x < th.Rect.Dx(); x++ {
			sum := uint32(0)
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					r, g, bl, _ := img.At(2*x+dx, 2*y+dy).RGBA()
					sum += (299*r + 587*g + 114*bl) / 1000 >> 8
				}
			}
			th.SetGray(x, y, color.Gray{Y: uint8(sum / 4)})
		}
	}
	return th
}

func encodeThumb(t *testing.T, th *image.Gray) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, th); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// diffThumbs returns how many pixels differ by more than tol, and the total.
func diffThumbs(a, b *image.Gray, tol int) (diff, total int) {
	total = a.Rect.Dx() * a.Rect.Dy()
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			d := int(a.GrayAt(x, y).Y) - int(b.GrayAt(x, y).Y)
			if d < 0 {
				d = -d
			}
			if d > tol {
				diff++
			}
		}
	}
	return diff, total
}

func compareGolden(t *testing.T, name string, rendered []byte) {
	t.Helper()
	th := goldenThumb(t, rendered)
	data := encodeThumb(t, th)
	if len(data) > goldenMaxBytes {
		t.Fatalf("golden %s too large: %d bytes", name, len(data))
	}
	path := filepath.Join("testdata", name+"_small.png")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (UPDATE_GOLDEN=1 to create): %v", name, err)
	}
	want, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	wg, ok := want.(*image.Gray)
	if !ok {
		t.Fatalf("golden %s is not grayscale", name)
	}
	if wg.Rect != th.Rect {
		t.Fatalf("golden %s size changed: %v -> %v", name, wg.Rect, th.Rect)
	}
	diff, total := diffThumbs(wg, th, goldenLumaTol)
	if pct := float64(diff) * 100 / float64(total); pct > goldenMaxDiffPct {
		t.Fatalf("golden %s: %d of %d pixels differ (%.2f%% > %.2f%%); UPDATE_GOLDEN=1 if intended", name, diff, total, pct, goldenMaxDiffPct)
	}
}

func TestGoldenToleranceHelper(t *testing.T) {
	a := image.NewGray(image.Rect(0, 0, 10, 10))
	b := image.NewGray(image.Rect(0, 0, 10, 10))
	b.SetGray(1, 1, color.Gray{Y: 10}) // within tolerance
	if d, _ := diffThumbs(a, b, goldenLumaTol); d != 0 {
		t.Fatalf("small drift must be tolerated, diff=%d", d)
	}
	b.SetGray(2, 2, color.Gray{Y: 200}) // a real change
	if d, tot := diffThumbs(a, b, goldenLumaTol); d != 1 || tot != 100 {
		t.Fatalf("diff=%d total=%d", d, tot)
	}
}

func TestGoldenRenders(t *testing.T) {
	cases := map[string]func() (Rendered, error){
		"intruder_race": func() (Rendered, error) { return RenderIntruderRace(raceFixture(), Opts{Width: 640}) },
		"intruder_strip": func() (Rendered, error) {
			rows := stripFixture(60)
			rows[5].Status, rows[9].Status, rows[20].Length = 429, 200, 4000
			rows[9].Matched = true
			return RenderIntruderStrip(StripInput{RunID: "run-example", Attack: "sniper", Rows: rows, Mask: true}, Opts{Width: 640})
		},
		"intruder_distribution": func() (Rendered, error) { return RenderIntruderDistribution(distFixture(), Opts{Width: 640}) },
		"flow_waterfall": func() (Rendered, error) {
			return RenderFlowWaterfall(WaterfallInput{Title: "Login sequence", Rows: wfRows()}, Opts{Width: 640})
		},
		"finding_chain": func() (Rendered, error) { return RenderFindingChain(longChain(7), Opts{Width: 640}) },
		"authz_matrix":  func() (Rendered, error) { return RenderAuthzMatrix(authzFixture(), Opts{Width: 640}) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := fn()
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, name, r.PNG)
		})
	}
	_ = fmt.Sprint
}
