package preview

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stripFixture(n int) []StripRow {
	rows := make([]StripRow, n)
	for i := range rows {
		rows[i] = StripRow{Seq: i + 1, Payload: fmt.Sprintf("user%03d", i), Status: 401, Length: 1200, TimeMs: 30}
	}
	return rows
}

func TestMaskPayload(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"a":                "*",
		"abc":              "***",
		"abcd":             "ab*d",
		"hunter2":          "hu****2",
		"123456789012":     "12*********2",
		"1234567890123456": "12*********6",
		"pässwörd":         "pä*****d",
	}
	for in, want := range cases {
		if got := maskPayload(in); got != want {
			t.Errorf("maskPayload(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLooksSecret(t *testing.T) {
	if !looksSecret("eyJhbGciOiJIUzI1NiJ9abc123") {
		t.Error("token-like should be secret")
	}
	for _, s := range []string{"admin", "john.doe@example.com", "aaaaaaaaaaaaaaaaaaaaaaaa", "a long sentence with 123 spaces in it"} {
		if looksSecret(s) {
			t.Errorf("%q should not be secret", s)
		}
	}
}

func TestStripOutliersExact(t *testing.T) {
	rows := stripFixture(100)
	rows[10].Status, rows[10].Length = 200, 5200
	rows[40].Matched = true
	rows[77].Anomaly = true
	out := stripOutliers(StripInput{Rows: rows})
	var got []string
	for _, o := range out {
		got = append(got, o.Payload)
	}
	want := []string{"user010", "user040", "user077"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("outliers %v want %v", got, want)
	}
}

func TestStripOutlierMaskedInAlt(t *testing.T) {
	rows := stripFixture(10)
	rows[3].Payload = "SuperSecretToken1234567890"
	rows[3].Status = 200
	r, err := RenderIntruderStrip(StripInput{Rows: rows}, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Alt, "SuperSecretToken") {
		t.Fatalf("alt leaks secret: %s", r.Alt)
	}
	if !strings.Contains(r.Alt, "Su*********0") {
		t.Fatalf("alt missing masked payload: %s", r.Alt)
	}
	r, _ = RenderIntruderStrip(StripInput{Rows: rows, Mask: true}, Opts{})
	if strings.Contains(r.Alt, "SuperSecretToken") || r.Kind != KindIntruderStrip {
		t.Fatalf("bad render %q", r.Alt)
	}
}

func TestStripCapAndAggregate(t *testing.T) {
	in := StripInput{Rows: stripFixture(500)}
	if n := stripPlotted(in, Opts{}); n != 400 {
		t.Fatalf("plotted=%d", n)
	}
	r, err := RenderIntruderStrip(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "+100 more") {
		t.Fatalf("summary %q", r.Summary)
	}
}

func TestStripDeterministic(t *testing.T) {
	rows := stripFixture(60)
	rows[5].Status = 429
	in := StripInput{RunID: "r1", Attack: "sniper", Rows: rows}
	a, _ := RenderIntruderStrip(in, Opts{})
	b, _ := RenderIntruderStrip(in, Opts{})
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) {
		t.Fatal("not deterministic")
	}
	if !strings.Contains(a.Alt, "60 payloads") {
		t.Fatalf("alt %q", a.Alt)
	}
}

func TestStripGlyphOnBlockedAndErrorCells(t *testing.T) {
	rows := stripFixture(20)
	rows[2].Status = 429
	rows[3].Status = 0
	rows[3].Length = 1200
	r, err := RenderIntruderStrip(StripInput{Rows: rows}, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	for _, idx := range []int{2, 3} {
		rc := stripCellRect(r.Width, idx)
		seen := map[[4]uint8]bool{}
		for y := rc.Min.Y; y < rc.Max.Y; y++ {
			for x := rc.Min.X; x < rc.Max.X; x++ {
				cr, cg, cb, ca := img.At(x, y).RGBA()
				seen[[4]uint8{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), uint8(ca >> 8)}] = true
			}
		}
		if len(seen) < 2 {
			t.Errorf("cell %d uniform, no glyph", idx)
		}
	}
	if r.Width != 1100 || r.Height <= 0 || r.Height > maxRenderHeight {
		t.Errorf("dims %dx%d", r.Width, r.Height)
	}
	_ = image.Pt
}

func TestStripEmpty(t *testing.T) {
	r, err := RenderIntruderStrip(StripInput{}, Opts{})
	if err != nil || len(r.PNG) == 0 {
		t.Fatal(err)
	}
}

func TestStripWriteSamples(t *testing.T) {
	dir := os.Getenv("STRIP_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set STRIP_SAMPLE_DIR to write samples")
	}
	_ = os.MkdirAll(dir, 0o755)
	rows := stripFixture(300)
	for i := range rows {
		if i%7 == 0 {
			rows[i].Length = 1200 + i
		}
	}
	rows[12].Status, rows[12].Length, rows[12].Matched = 200, 5230, true
	rows[120].Payload, rows[120].Status = "alpha.bravo.charlie@example.com", 302
	rows[121].Status, rows[121].Payload = 429, "hunter2hunter2hunter2hunter2"
	rows[122].Status = 0
	rows[123].Status, rows[123].Anomaly = 500, true
	for i := 250; i < 300; i++ {
		rows[i].Status = 429
		rows[i].Length = 310
	}
	for name, in := range map[string]StripInput{
		"many":  {RunID: "run-example", Attack: "sniper", Rows: rows},
		"500":   {RunID: "run-example", Attack: "sniper", Rows: stripFixture(500)},
		"one":   {Attack: "sniper", Rows: stripFixture(1)},
		"empty": {},
	} {
		r, err := RenderIntruderStrip(in, Opts{})
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, name+".png"), r.PNG, 0o644)
	}
}
