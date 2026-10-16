package preview

import (
	"fmt"
	"strings"
	"testing"
)

func TestAuthzUnclassifiedIsNotReassuring(t *testing.T) {
	in := AuthzMatrixInput{BaselineName: "admin", Cols: []string{"admin"}}
	for i := 0; i < 3; i++ {
		in.Rows = append(in.Rows, AuthzRow{Label: fmt.Sprintf("GET /r/%d", i), Cells: []AuthzCell{{Status: 200, Length: 100}}})
	}
	r, err := RenderAuthzMatrix(in, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Summary, "0 broken, 3 unclassified of 3") {
		t.Fatalf("summary: %s", r.Summary)
	}
	pal := lightReportPalette()
	l := authzPlan(in, authzMaxRows)
	if got := authzAccent(pal, l); got == pal.success {
		t.Fatal("green accent with unclassified cells gives false assurance")
	}
	clean := authzPlan(authzFixture(), authzMaxRows)
	clean.broken = 0
	if got := authzAccent(pal, clean); got != pal.success {
		t.Fatalf("fully classified clean run should be green, got %v", got)
	}
	if got := authzAccent(pal, authzPlan(authzFixture(), authzMaxRows)); got != pal.blocked {
		t.Fatalf("broken run must be red, got %v", got)
	}
}

func TestShortLength(t *testing.T) {
	cases := map[int]string{0: "0", 740: "740", 999: "999", 1000: "1k", 1234: "1.2k", 9999: "10k", 12345: "12k", 1500000: "1.5M"}
	for n, want := range cases {
		if got := shortLength(n); got != want {
			t.Errorf("shortLength(%d)=%q want %q", n, got, want)
		}
	}
}

func TestAuthzHeadersStayDistinguishable(t *testing.T) {
	c := newCanvas(300, 40, lightReportPalette())
	defer c.close()
	cols := []string{"identity-0-longname-admin", "identity-1-longname-admin", "identity-2-longname-admin"}
	widths := []int{90, 90, 90}
	labels, truncated := authzHeaders(c, cols, widths)
	if !truncated {
		t.Fatal("expected truncation")
	}
	seen := map[string]bool{}
	for i, l := range labels {
		if seen[l] {
			t.Fatalf("headers collapsed: %v", labels)
		}
		seen[l] = true
		if c.measure(fontBold, authzFont, l) > widths[i]-16 {
			t.Fatalf("label %q too wide", l)
		}
	}
}

func TestAuthzMiddleTruncation(t *testing.T) {
	c := newCanvas(300, 40, lightReportPalette())
	defer c.close()
	got := c.truncateMiddle(fontBold, authzFont, "identity-0-longname-admin", 110)
	if !strings.Contains(got, "...") || !strings.HasSuffix(got, "min") || !strings.HasPrefix(got, "iden") {
		t.Fatalf("got %q", got)
	}
	if c.truncateMiddle(fontBold, authzFont, "ok", 110) != "ok" {
		t.Fatal("short strings must pass through")
	}
}
