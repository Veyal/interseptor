package preview

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"
)

// focusFixture is a status line, three headers, a blank line and n body lines
// `  "k<i>": <i>,` (logical line numbers: status 1, headers 2-4, blank 5, body 6..).
func focusFixture(n int) []string {
	lines := []string{"HTTP/1.1 200 OK", "Content-Type: application/json", "X-Request-Id: abc", "Cache-Control: no-store", ""}
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf(`  "k%d": %d,`, i, i))
	}
	return lines
}

// hitsAt highlights the whole of the given 0-based lines.
func hitsAt(lines []string, idx ...int) paneFocus {
	f := paneFocus{hits: make([][]span, len(lines))}
	for _, i := range idx {
		f.hits[i] = []span{{0, len(lines[i])}}
	}
	return f
}

// rowsText renders a plan as "no|text" lines for compact assertions.
func rowsText(rows []planRow) string {
	var b strings.Builder
	for _, r := range rows {
		switch r.kind {
		case rowMarker:
			b.WriteString("  |" + r.text)
		case rowCont:
			b.WriteString("  ↳|" + r.text)
		default:
			fmt.Fprintf(&b, "%d|%s", r.lineNo, r.text)
		}
		if r.match() {
			b.WriteString(" [hl]")
		}
		if r.inRange {
			b.WriteString(" [range]")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPlanFocusCollapsesHeadersAndAddsGaps(t *testing.T) {
	lines := focusFixture(100) // body 0-based 5..104
	got := rowsText(planFocus(lines, hitsAt(lines, 60), 2, 80, 100))
	want := `1|HTTP/1.1 200 OK
  |… 3 headers hidden …
  |… 53 lines hidden (6–58) …
59|  "k53": 53,
60|  "k54": 54,
61|  "k55": 55, [hl]
62|  "k56": 56,
63|  "k57": 57,
  |… 42 lines hidden (64–105) …
`
	if got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanFocusMergesNearbyWindows(t *testing.T) {
	lines := focusFixture(100)
	cases := map[string]struct {
		hits []int
		want []string // gap markers expected between and after the windows
	}{
		"overlap":      {[]int{60, 64}, []string{"… 3 headers hidden …", "… 53 lines hidden (6–58) …", "… 38 lines hidden (68–105) …"}},
		"adjacent":     {[]int{60, 65}, []string{"… 3 headers hidden …", "… 53 lines hidden (6–58) …", "… 37 lines hidden (69–105) …"}},
		"one-line gap": {[]int{60, 66}, []string{"… 3 headers hidden …", "… 53 lines hidden (6–58) …", "… 36 lines hidden (70–105) …"}},
		"real gap":     {[]int{60, 67}, []string{"… 3 headers hidden …", "… 53 lines hidden (6–58) …", "… 2 lines hidden (64–65) …", "… 35 lines hidden (71–105) …"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var markers []string
			for _, r := range planFocus(lines, hitsAt(lines, c.hits...), 2, 80, 100) {
				if r.kind == rowMarker {
					markers = append(markers, r.text)
				}
			}
			if strings.Join(markers, "|") != strings.Join(c.want, "|") {
				t.Fatalf("markers %q want %q", markers, c.want)
			}
		})
	}
}

func TestPlanFocusShowsHeaderBlockWhenMatchIsAHeader(t *testing.T) {
	lines := focusFixture(30)
	got := rowsText(planFocus(lines, hitsAt(lines, 2), 1, 80, 100))
	want := `1|HTTP/1.1 200 OK
2|Content-Type: application/json
3|X-Request-Id: abc [hl]
4|Cache-Control: no-store
  |… 31 lines hidden (5–35) …
`
	if got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanFocusFirstLineMatchKeepsHeadersCollapsed(t *testing.T) {
	lines := focusFixture(10)
	got := rowsText(planFocus(lines, hitsAt(lines, 0), 3, 80, 100))
	want := `1|HTTP/1.1 200 OK [hl]
  |… 3 headers hidden …
  |… 10 lines hidden (6–15) …
`
	if got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanFocusCompactWithoutAnchors(t *testing.T) {
	lines := focusFixture(10)
	got := rowsText(planFocus(lines, paneFocus{}, 6, 80, 100))
	want := `1|HTTP/1.1 200 OK
  |… 3 headers hidden …
  |… 10 lines hidden (6–15) …
`
	if got != want {
		t.Fatalf("compact plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanFocusRespectsBudgetAndCountsUnshownMatches(t *testing.T) {
	lines := focusFixture(200)
	var idx []int
	for i := 10; i < 200; i += 20 {
		idx = append(idx, 5+i)
	}
	rows := planFocus(lines, hitsAt(lines, idx...), 2, 20, 100)
	if len(rows) > 20 {
		t.Fatalf("plan uses %d rows, budget 20:\n%s", len(rows), rowsText(rows))
	}
	last := rows[len(rows)-1]
	if last.kind != rowMarker || !strings.Contains(last.text, "more matches not shown") {
		t.Fatalf("plan must end with a more-matches marker:\n%s", rowsText(rows))
	}
	// Earlier matches win: the first match (logical line 16) is shown.
	if !strings.Contains(rowsText(rows), "16|  \"k10\": 10, [hl]") {
		t.Fatalf("first match missing:\n%s", rowsText(rows))
	}
}

func TestPlanFocusLineRangesMarkGutterOnly(t *testing.T) {
	lines := focusFixture(50)
	f := paneFocus{hits: make([][]span, len(lines)), ranges: [][2]int{{20, 21}}}
	got := rowsText(planFocus(lines, f, 0, 80, 100))
	want := `1|HTTP/1.1 200 OK
  |… 3 headers hidden …
  |… 15 lines hidden (6–20) …
21|  "k15": 15, [range]
22|  "k16": 16, [range]
  |… 33 lines hidden (23–55) …
`
	if got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanWrapsHighlightAcrossContinuationRows(t *testing.T) {
	lines := []string{"GET / HTTP/1.1", "", "aaaaaaaaSECRETbbbb"}
	f := paneFocus{hits: [][]span{nil, nil, {{8, 14}}}}
	rows := planFocus(lines, f, 0, 80, 10)
	got := rowsText(rows)
	want := `1|GET / HTTP
  ↳|/1.1
2|
3|aaaaaaaaSE [hl]
  ↳|CRETbbbb [hl]
`
	if got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
	if h := rows[3].hl; len(h) != 1 || h[0] != (span{8, 10}) {
		t.Fatalf("first row span %v", h)
	}
	if h := rows[4].hl; len(h) != 1 || h[0] != (span{0, 4}) || rows[4].lineNo != 3 {
		t.Fatalf("continuation span %v line %d", h, rows[4].lineNo)
	}
}

func TestWrapSegmentsCountsRunesNotBytes(t *testing.T) {
	segs := wrapSegments("ééééé", 2)
	if len(segs) != 3 || segs[0] != (span{0, 4}) || segs[2] != (span{8, 10}) {
		t.Fatalf("segments %v", segs)
	}
}

func TestLiteralFocusToleratesJSONSpacing(t *testing.T) {
	re, err := compileFocus([]FocusSpec{{Text: `"isAdmin":true`}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{`  "isAdmin": true,`, `{"ISADMIN":TRUE}`} {
		if !re[0].MatchString(line) {
			t.Fatalf("literal focus should match %q", line)
		}
	}
	if re[0].MatchString(`"isAdmin": false`) {
		t.Fatal("literal focus matched a different value")
	}
	re, _ = compileFocus([]FocusSpec{{Text: "a.b"}})
	if re[0].MatchString("axb") {
		t.Fatal("literal focus must quote regex metacharacters")
	}
}

func TestValidateFocusRejectsBadInput(t *testing.T) {
	long := strings.Repeat("x", MaxFocusTextLen+1)
	many := make([]FocusSpec, MaxFocusSpecs+1)
	for i := range many {
		many[i] = FocusSpec{Text: "x"}
	}
	for name, o := range map[string]Options{
		"bad regex":  {Focus: []FocusSpec{{Text: "(", Regex: true}}},
		"long text":  {Focus: []FocusSpec{{Text: long}}},
		"empty text": {Focus: []FocusSpec{{Text: "  "}}},
		"too many":   {Focus: many},
		"bad range":  {Lines: []LineRange{{From: 5, To: 2}}},
	} {
		if err := ValidateFocus(o); !errors.Is(err, ErrInvalidFocus) {
			t.Fatalf("%s: want ErrInvalidFocus, got %v", name, err)
		}
	}
	if _, err := RenderFlow(nil, nil, Options{Focus: []FocusSpec{{Text: "[", Regex: true}}}); !errors.Is(err, ErrInvalidFocus) {
		t.Fatalf("render with a bad regex: %v", err)
	}
}

func TestParseLineRanges(t *testing.T) {
	got, err := ParseLineRanges("res:400-440, req:1-5,12")
	if err != nil {
		t.Fatal(err)
	}
	want := []LineRange{{SideRes, 400, 440}, {SideReq, 1, 5}, {SideBoth, 12, 12}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, bad := range []string{"x:1-2", "5-3", "0-3", "res:a-b", "res:"} {
		if _, err := ParseLineRanges(bad); !errors.Is(err, ErrInvalidFocus) {
			t.Fatalf("%q: want ErrInvalidFocus, got %v", bad, err)
		}
	}
}

func longJSONResponse(n, adminAt int) []byte {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nSet-Cookie: sid=example-secret\r\n\r\n{\"users\":[")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		admin := "false"
		if i == adminAt {
			admin = "true"
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user%d","isAdmin":%s}`, i, i, admin)
	}
	b.WriteString("]}")
	return []byte(b.String())
}

func TestRenderFlowReportsMatchesAndMode(t *testing.T) {
	req := []byte("GET /api/users HTTP/1.1\r\nHost: example.com\r\n\r\n")
	res := longJSONResponse(120, 80)
	r, err := RenderFlow(req, res, Options{Focus: []FocusSpec{{Text: `"isAdmin":true`}}, Caption: "Highlighted: admin flag"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeFocus || r.Matches[SideRes] != 1 || r.Matches[SideReq] != 0 || len(r.NotFound) != 0 {
		t.Fatalf("result %+v", r)
	}
	if len(r.PNG) < 200 {
		t.Fatal("no PNG")
	}
}

func TestRenderFlowNotFoundFallsBackToTopWithNotice(t *testing.T) {
	res := longJSONResponse(10, -1)
	o := Options{Side: SideRes, Focus: []FocusSpec{{Text: "nowhere"}}, Lines: []LineRange{{SideRes, 900, 950}}}
	panes, bands, r, err := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeTop || fmt.Sprint(r.NotFound) != "[nowhere res:900-950]" {
		t.Fatalf("result %+v", r)
	}
	if len(bands) != 1 || !bands[0].notice || !strings.Contains(bands[0].text, "Focus text not found: “nowhere”") {
		t.Fatalf("bands %+v", bands)
	}
	if panes[0].rows[0].lineNo != 1 || panes[0].rows[1].lineNo != 2 {
		t.Fatalf("top mode should start at line 1:\n%s", rowsText(panes[0].rows))
	}
}

func TestRenderFlowMaskedMatchIsReportedNotUnmasked(t *testing.T) {
	res := longJSONResponse(3, -1)
	mask := func(s string) string { return strings.ReplaceAll(s, "sid=example-secret", "sid=****") }
	o := Options{Side: SideRes, Focus: []FocusSpec{{Text: "example-secret"}}, Mask: mask}
	panes, bands, r, err := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if !r.MaskedMatch || len(r.NotFound) != 0 { // a masked-only text is never echoed
		t.Fatalf("result %+v", r)
	}
	if strings.Contains(rowsText(panes[0].rows), "example-secret") {
		t.Fatal("masked value leaked into the plan")
	}
	if len(bands) != 1 || !strings.Contains(bands[0].text, "masked") {
		t.Fatalf("bands %+v", bands)
	}
}

func TestRenderFlowOtherSidePaneStaysTopMode(t *testing.T) {
	req := []byte("POST /api/users HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n{\"name\":\"a\"}")
	res := longJSONResponse(60, 40)
	o := Options{Focus: []FocusSpec{{Side: SideRes, Text: `"isAdmin": true`}}}
	panes, _, r, err := planFlow(req, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeFocus {
		t.Fatalf("mode %s", r.Mode)
	}
	if strings.Contains(rowsText(panes[0].rows), "hidden") {
		t.Fatalf("request pane was not searched and should render top mode:\n%s", rowsText(panes[0].rows))
	}
	// Searched without a hit: a short pane stays whole, a long one is compact.
	o.Focus = append(o.Focus, FocusSpec{Side: SideReq, Text: "absent"})
	panes, _, _, _ = planFlow(req, res, o.normalized(), themePalette(ThemeLight))
	if got := rowsText(panes[0].rows); strings.Contains(got, "hidden") {
		t.Fatalf("a short searched request pane should stay whole:\n%s", got)
	}
	longReq := []byte("POST /api/users HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n[" + strings.Repeat("1,", 30) + "1]")
	panes, _, _, _ = planFlow(longReq, res, o.normalized(), themePalette(ThemeLight))
	if got := rowsText(panes[0].rows); !strings.Contains(got, "2 headers hidden") || !strings.Contains(got, "lines hidden") || len(panes[0].rows) != 3 {
		t.Fatalf("a long searched request pane should be compact:\n%s", got)
	}
}

func TestPrettyJSONKeepsOrderAndMarkup(t *testing.T) {
	out := prettyHTTP([]byte("HTTP/1.1 200 OK\r\n\r\n{\"b\":1,\"a\":\"<script>\",\"n\":12345678901234567890}"))
	want := "{\n  \"b\": 1,\n  \"a\": \"<script>\",\n  \"n\": 12345678901234567890\n}"
	if !bytes.HasSuffix(out, []byte(want)) {
		t.Fatalf("pretty body:\n%s", out)
	}
}

func TestRenderFlowHonoursDeadline(t *testing.T) {
	_, err := RenderFlow(nil, []byte("HTTP/1.1 200 OK\r\n\r\n"), Options{Deadline: time.Now().Add(-time.Second)})
	if !errors.Is(err, ErrRenderTimeout) {
		t.Fatalf("expired deadline: %v", err)
	}
}

func relLum(c color.RGBA) float64 {
	ch := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

func contrast(a, b color.RGBA) float64 {
	x, y := relLum(a), relLum(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

func TestFocusPaletteContrast(t *testing.T) {
	for _, th := range []Theme{ThemeLight, ThemeDark} {
		p := themePalette(th)
		checks := []struct {
			name   string
			fg, bg color.RGBA
			min    float64
		}{
			{"highlighted text", p.hlText, p.hl, 7},
			{"marker text", p.muted, p.marker, 4.5},
			{"notice", p.notice, p.bg, 4.5},
			{"line numbers on response", p.muted, p.resTint, 4.5},
		}
		for _, c := range checks {
			if got := contrast(c.fg, c.bg); got < c.min {
				t.Errorf("%s %s contrast %.2f < %.1f", th, c.name, got, c.min)
			}
		}
	}
}

func countColor(t *testing.T, data []byte, c color.RGBA) int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == c.R && uint8(g>>8) == c.G && uint8(bl>>8) == c.B {
				n++
			}
		}
	}
	return n
}

func TestRenderFlowDrawsHighlighterBoxPerTheme(t *testing.T) {
	res := longJSONResponse(30, 20)
	for _, th := range []Theme{ThemeLight, ThemeDark} {
		pal := themePalette(th)
		focused, err := RenderFlow(nil, res, Options{Side: SideRes, Theme: th, Focus: []FocusSpec{{Text: `"isAdmin":true`}}})
		if err != nil {
			t.Fatal(err)
		}
		if n := countColor(t, focused.PNG, pal.hl); n < 100 {
			t.Fatalf("%s: highlighter box missing (%d px)", th, n)
		}
		plain, _ := RenderFlow(nil, res, Options{Side: SideRes, Theme: th})
		if n := countColor(t, plain.PNG, pal.hl); n != 0 {
			t.Fatalf("%s: top mode drew %d highlight px", th, n)
		}
	}
}
