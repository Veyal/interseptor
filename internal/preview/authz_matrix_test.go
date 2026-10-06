package preview

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

func authzFixture() AuthzMatrixInput {
	ok := func(l int) AuthzCell { return AuthzCell{Status: 200, Length: l, SameAsBaseline: true, FlowID: 1} }
	den := AuthzCell{Status: 403, Length: 40, AccessDenied: true}
	brk := func(l int) AuthzCell { return AuthzCell{Status: 200, Length: l, Broken: true} }
	return AuthzMatrixInput{
		RunID: "run-1", BaselineName: "admin",
		Cols: []string{"admin", "user-a", "anonymous"},
		Rows: []AuthzRow{
			{Label: "GET /api/users/1", Cells: []AuthzCell{ok(500), brk(500), den}},
			{Label: "GET /api/orders", Cells: []AuthzCell{ok(900), den, den}},
			{Label: "DELETE /api/orders/7", Cells: []AuthzCell{ok(20), den, brk(20)}},
			{Label: "GET /api/me", Cells: []AuthzCell{ok(80), ok(80), {Status: 401, SessionInvalid: true}}},
		},
	}
}

func TestAuthzMatrixBrokenCells(t *testing.T) {
	r, err := RenderAuthzMatrix(authzFixture(), Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindAuthzMatrix {
		t.Fatalf("kind %q", r.Kind)
	}
	if !strings.Contains(r.Summary, "2 broken of 12") {
		t.Fatalf("summary %q", r.Summary)
	}
	for _, w := range []string{"GET /api/users/1", "user-a", "DELETE /api/orders/7", "anonymous"} {
		if !strings.Contains(r.Alt, w) {
			t.Fatalf("alt missing %q: %s", w, r.Alt)
		}
	}
	if strings.Contains(r.Alt, "GET /api/orders,") || strings.Count(r.Alt, "broken") < 1 {
		t.Fatalf("alt: %s", r.Alt)
	}
	img, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil || img.Bounds().Dx() != r.Width || img.Bounds().Dy() != r.Height {
		t.Fatalf("decode/dims: %v", err)
	}
}

func TestAuthzMatrixDeterministic(t *testing.T) {
	a, _ := RenderAuthzMatrix(authzFixture(), Opts{})
	b, _ := RenderAuthzMatrix(authzFixture(), Opts{})
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) {
		t.Fatal("hash differs between renders")
	}
}

func TestAuthzMatrixOverflowNote(t *testing.T) {
	in := AuthzMatrixInput{BaselineName: "c0"}
	for i := 0; i < 11; i++ {
		in.Cols = append(in.Cols, fmt.Sprintf("id%d", i))
	}
	for r := 0; r < 45; r++ {
		row := AuthzRow{Label: fmt.Sprintf("GET /r/%d", r)}
		for range in.Cols {
			row.Cells = append(row.Cells, AuthzCell{Status: 200, Length: 10, Broken: r == 44})
		}
		in.Rows = append(in.Rows, row)
	}
	r, err := RenderAuthzMatrix(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Alt, "+3 more identities") || !strings.Contains(r.Alt, "+5 more rows") {
		t.Fatalf("alt lacks overflow note: %s", r.Alt)
	}
	if !strings.Contains(r.Summary, "11 broken of 495") {
		t.Fatalf("summary counts all cells: %s", r.Summary)
	}
	if r.Height > maxRenderHeight {
		t.Fatalf("height %d", r.Height)
	}
}

func TestAuthzMatrixEmpty(t *testing.T) {
	r, err := RenderAuthzMatrix(AuthzMatrixInput{}, Opts{})
	if err != nil || len(r.PNG) == 0 || !strings.Contains(r.Summary, "0 broken of 0") {
		t.Fatalf("empty: %v %q", err, r.Summary)
	}
}

func TestAuthzMatrixBrokenCellTinted(t *testing.T) {
	in := authzFixture()
	r, _ := RenderAuthzMatrix(in, Opts{})
	img, _ := png.Decode(bytes.NewReader(r.PNG))
	// scan for the broken tint colour; it must exist, and be absent when nothing is broken.
	has := func(r Rendered) bool {
		im, _ := png.Decode(bytes.NewReader(r.PNG))
		want := authzBrokenTint(lightReportPalette())
		b := im.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				cr, cg, cb, _ := im.At(x, y).RGBA()
				if uint8(cr>>8) == want.R && uint8(cg>>8) == want.G && uint8(cb>>8) == want.B {
					return true
				}
			}
		}
		return false
	}
	_ = img
	if !has(r) {
		t.Fatal("broken tint not drawn")
	}
	for i := range in.Rows {
		for j := range in.Rows[i].Cells {
			in.Rows[i].Cells[j].Broken = false
		}
	}
	r2, _ := RenderAuthzMatrix(in, Opts{})
	if has(r2) {
		t.Fatal("tint drawn without broken cells")
	}
}
