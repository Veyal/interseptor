package preview

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func hostileAuthz(rows, cols, labelLen int) AuthzMatrixInput {
	in := AuthzMatrixInput{RunID: "r", BaselineName: "admin"}
	for i := 0; i < cols; i++ {
		in.Cols = append(in.Cols, "identity-"+strings.Repeat("x", 8))
	}
	label := "GET /" + strings.Repeat("a", labelLen)
	cells := make([]AuthzCell, cols)
	for i := range cells {
		cells[i] = AuthzCell{Status: 200, Broken: true}
	}
	in.Rows = make([]AuthzRow, rows)
	for i := range in.Rows {
		in.Rows[i] = AuthzRow{Label: label, Cells: cells}
	}
	return in
}

// The alt text names at most 20 broken cells. Building it must cost in
// proportion to those 20, not to every broken cell of the run.
func TestAuthzAltWorkIsBoundedByTheCellCap(t *testing.T) {
	in := hostileAuthz(2000, 40, 10_000) // 80,000 broken cells, 10 KB labels
	l := authzPlan(in, authzMaxRows)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	alt := authzAlt(in, l)
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 4<<20 {
		t.Errorf("authzAlt allocated %d MiB for a 20-cell list", got>>20)
	}
	if !strings.Contains(alt, "and 79980 more") {
		t.Errorf("alt text lost the count of cells not listed: %.200s", alt[len(alt)-min(len(alt), 200):])
	}
	if n := strings.Count(alt, "(status 200)"); n != 20 {
		t.Errorf("alt text lists %d cells, want 20", n)
	}
	if len(alt) > 16<<10 {
		t.Errorf("alt text is %d bytes", len(alt))
	}
}

// The probe from the round-2 report: 100,000 rows x 40 identities with 100 KB
// labels. The renderer must finish on its own, without the adapter's caps.
func TestAuthzMatrixRendersUncappedInput(t *testing.T) {
	in := hostileAuthz(100_000, 40, 100_000)
	start := time.Now()
	r, err := RenderAuthzMatrix(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("render took %s", d)
	}
	if !strings.Contains(r.Alt, "and 3999980 more") || !strings.Contains(r.Summary, "4000000 broken") {
		t.Errorf("counts wrong: summary %q", r.Summary)
	}
	t.Logf("rendered in %s, alt %d bytes", time.Since(start), len(r.Alt))
}

func TestAuthzAltUnchangedForOrdinaryInput(t *testing.T) {
	in := AuthzMatrixInput{BaselineName: "admin", Cols: []string{"admin", "user"}, Rows: []AuthzRow{
		{Label: "GET /a", Cells: []AuthzCell{{Status: 200, SameAsBaseline: true}, {Status: 200, Broken: true}}},
		{Label: "GET /b", Cells: []AuthzCell{{Status: 200, SameAsBaseline: true}, {Status: 403, AccessDenied: true}, {Status: 200, Broken: true}}},
	}}
	got := authzAlt(in, authzPlan(in, authzMaxRows))
	want := "Authorization differential matrix, 2 identities by 2 requests, baseline admin. 1 broken, 0 unclassified of 4 cells (2 identities x 2 rows). Broken access: GET /a as user (status 200)."
	if got != want {
		t.Errorf("alt\n got %q\nwant %q", got, want)
	}
}
