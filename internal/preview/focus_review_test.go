package preview

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// longLineFixture is a response whose body is one minified line of n bytes
// with needle placed at byte offset at (logical line 4).
func longLineFixture(n, at int, needle string) []string {
	body := []byte(strings.Repeat("a", n))
	copy(body[at:], needle)
	return []string{"HTTP/1.1 200 OK", "Content-Type: application/json", "", string(body)}
}

// needleHits highlights every occurrence of needle.
func needleHits(lines []string, needle string) paneFocus {
	f := paneFocus{hits: make([][]span, len(lines))}
	re := regexp.MustCompile(regexp.QuoteMeta(needle))
	for i, l := range lines {
		for _, m := range re.FindAllStringIndex(l, -1) {
			f.hits[i] = append(f.hits[i], span{m[0], m[1]})
		}
	}
	return f
}

func TestPlanTopMarksRowsCutFromALongLine(t *testing.T) {
	lines := longLineFixture(20000, 0, "")
	rows := planTop(lines, paneFocus{}, 10, 100)
	got := rowsText(rows)
	if len(rows) > 10 {
		t.Fatalf("plan uses %d rows, budget 10:\n%s", len(rows), got)
	}
	last := rows[len(rows)-1]
	if last.kind != rowMarker || strings.Contains(last.text, "0 more lines") || !strings.Contains(last.text, "chars of line 4") {
		t.Fatalf("a cut long line needs a chars marker, not \"0 more lines\":\n%s", got)
	}
	// 6 rows of line 4 shown (100 chars each) → 19400 chars hidden.
	if !strings.Contains(last.text, "19400") {
		t.Fatalf("hidden char count wrong:\n%s", got)
	}
}

func TestPlanTopCountsCutLineAndMoreLines(t *testing.T) {
	lines := append(longLineFixture(5000, 0, ""), "tail1", "tail2")
	rows := planTop(lines, paneFocus{}, 10, 100)
	last := rows[len(rows)-1].text
	if !strings.Contains(last, "chars of line 4") || !strings.Contains(last, "2 more lines") {
		t.Fatalf("marker %q", last)
	}
}

func TestPlanFocusCentersOnHitInsideALongLine(t *testing.T) {
	lines := longLineFixture(20000, 19000, `"isAdmin":true`)
	f := needleHits(lines, `"isAdmin":true`)
	rows := planFocus(lines, f, 2, 20, 100)
	got := rowsText(rows)
	if len(rows) > 20 {
		t.Fatalf("plan uses %d rows, budget 20:\n%s", len(rows), got)
	}
	hl := 0
	for _, r := range rows {
		if r.match() {
			hl++
		}
	}
	if hl == 0 {
		t.Fatalf("the match must be drawn:\n%s", got)
	}
	if !strings.Contains(got, "chars of line 4 hidden") {
		t.Fatalf("hidden part before the hit needs a marker:\n%s", got)
	}
	// The window starts near the hit (segment 190), not at the line start.
	for _, r := range rows {
		if r.kind == rowLine && r.lineNo == 4 {
			t.Fatalf("the line start should be hidden behind a marker:\n%s", got)
		}
	}
	if strings.Contains(got, "more match") {
		t.Fatalf("the only match is drawn:\n%s", got)
	}
	// A few segments of context, not the whole budget.
	if n := strings.Count(got, "↳|"); n > clipLead+1+clipTail {
		t.Fatalf("clip keeps %d segments, want at most %d:\n%s", n, clipLead+1+clipTail, got)
	}
}

func TestPlanFocusLongLineMarksHiddenTail(t *testing.T) {
	lines := longLineFixture(20000, 500, "NEEDLE")
	rows := planFocus(lines, needleHits(lines, "NEEDLE"), 2, 20, 100)
	got := rowsText(rows)
	if !strings.Contains(got, "more chars of line 4 hidden") {
		t.Fatalf("the hidden tail needs a marker:\n%s", got)
	}
	if strings.Contains(got, "0 more lines") {
		t.Fatalf("bogus zero marker:\n%s", got)
	}
}

func TestPlanFocusCountsUndrawnHitsOnTheCutLine(t *testing.T) {
	body := []byte(strings.Repeat("a", 20000))
	for _, at := range []int{300, 12000, 19000} {
		copy(body[at:], "NEEDLE")
	}
	lines := []string{"HTTP/1.1 200 OK", "", string(body)}
	rows := planFocus(lines, needleHits(lines, "NEEDLE"), 2, 12, 100)
	got := rowsText(rows)
	if !strings.Contains(got, "2 more matches not shown") {
		t.Fatalf("hits on the cut line must be counted:\n%s", got)
	}
}

func TestRenderFlowReportsMatchesItCouldNotDraw(t *testing.T) {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	b.WriteString("NEEDLE\n")
	o := Options{Side: SideRes, MaxLines: 4, Context: Int(0), Focus: []FocusSpec{{Text: "NEEDLE"}}}
	_, _, r, err := planFlow(nil, []byte(b.String()), o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if r.Matches[SideRes] != 1 {
		t.Fatalf("result %+v", r)
	}
	if fmt.Sprint(r.NotShown) != "[res]" {
		t.Fatalf("a match that is not drawn must be reported as NotShown: %+v", r)
	}
	o.MaxLines = 80
	_, _, r, _ = planFlow(nil, []byte(b.String()), o.normalized(), themePalette(ThemeLight))
	if len(r.NotShown) != 0 {
		t.Fatalf("drawn match reported as not shown: %+v", r)
	}
}

func TestPlanFocusKeepsRangeOnHeaderBodySeparator(t *testing.T) {
	lines := focusFixture(20) // blank separator is logical line 5
	f := paneFocus{hits: make([][]span, len(lines)), ranges: [][2]int{{4, 4}}}
	got := rowsText(planFocus(lines, f, 0, 80, 100))
	if !strings.Contains(got, "5| [range]") {
		t.Fatalf("the separator row must be drawn with the range bar:\n%s", got)
	}
	got = rowsText(planFocus(lines, f, 2, 80, 100))
	if !strings.Contains(got, "5| [range]") || !strings.Contains(got, "7|") {
		t.Fatalf("separator range with context:\n%s", got)
	}
}

func TestLiteralFocusToleratesJSONUnicodeEscapes(t *testing.T) {
	cases := []struct{ focus, line string }{
		{`<script>alert(1)</script>`, `  "bio": "\u003cscript\u003ealert(1)\u003c/script\u003e",`},
		{`a&b`, `  "q": "a\u0026b"`},
		{`\u003cb\u003e`, `  "html": "<b>"`},
		{`<b>`, `  "html": "\u003Cb\u003E"`},
	}
	for _, c := range cases {
		re, err := focusRegexp(FocusSpec{Text: c.focus})
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(c.line) {
			t.Fatalf("%q should match %q", c.focus, c.line)
		}
	}
}

func TestRenderFlowMaskedMatchDoesNotPrintFocusText(t *testing.T) {
	res := longJSONResponse(3, -1)
	mask := func(s string) string { return strings.ReplaceAll(s, "sid=example-secret", "sid=****") }
	o := Options{Side: SideRes, Focus: []FocusSpec{{Text: "example-secret"}}, Mask: mask}
	_, bands, r, err := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if !r.MaskedMatch {
		t.Fatalf("result %+v", r)
	}
	if strings.Contains(fmt.Sprint(r.NotFound), "example-secret") {
		t.Fatalf("NotFound echoes the masked focus text: %+v", r.NotFound)
	}
	for _, b := range bands {
		if strings.Contains(b.text, "example-secret") {
			t.Fatalf("band echoes the masked focus text: %q", b.text)
		}
	}
	if len(bands) != 1 || !strings.Contains(bands[0].text, "Focus matched a masked value (hidden)") {
		t.Fatalf("bands %+v", bands)
	}
	// A second, genuinely absent text is still reported by name.
	o.Focus = append(o.Focus, FocusSpec{Text: "nowhere"})
	_, bands, r, _ = planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if fmt.Sprint(r.NotFound) != "[nowhere]" || !strings.Contains(bands[0].text, "“nowhere”") || strings.Contains(bands[0].text, "example-secret") {
		t.Fatalf("result %+v bands %+v", r, bands)
	}
}

// bigJSON is a compact JSON body of n user objects, marker on item at.
func bigJSON(n, at int) string {
	var b strings.Builder
	b.WriteString(`{"users":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user%d","isAdmin":%t}`, i, i, i == at)
	}
	b.WriteString("]}")
	return b.String()
}

func TestRenderFlowBodyCapPrettyPrintsThenCuts(t *testing.T) {
	body := bigJSON(2500, 10) // ~110 KiB compact
	res := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n" + body)
	o := Options{Side: SideRes, BodyCap: 64 << 10, BodySize: map[Side]int64{SideRes: int64(len(body))}, Focus: []FocusSpec{{Text: `"name": "user10"`}}}
	panes, _, r, err := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if r.Matches[SideRes] != 1 || fmt.Sprint(r.BodyTruncated) != "[res]" {
		t.Fatalf("result %+v", r)
	}
	got := rowsText(panes[0].rows)
	// Pretty-printed: the match sits on its own short numbered line.
	if !strings.Contains(got, `|      "name": "user10", [hl]`) {
		t.Fatalf("body should be pretty-printed before the cut:\n%s", got)
	}
	if !strings.Contains(got, "Body truncated at 64 KiB for the image (") || !strings.Contains(got, "KiB total)") {
		t.Fatalf("truncation notice row missing:\n%s", got)
	}
	// Beyond the cap: not found, and the band says the body was cut.
	o.Focus = []FocusSpec{{Text: `"name": "user2400"`}}
	_, bands, r, _ := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if r.Mode != ModeTop || len(r.NotFound) != 1 {
		t.Fatalf("result %+v", r)
	}
	if len(bands) == 0 || !strings.Contains(bands[0].text, "64 KiB") {
		t.Fatalf("not-found band should mention the cut: %+v", bands)
	}
}

func TestRenderFlowSourceCutBodyIsReported(t *testing.T) {
	body := strings.Repeat("x", 1000)
	res := []byte("HTTP/1.1 200 OK\r\n\r\n" + body)
	o := Options{Side: SideRes, BodyCap: 64 << 10, BodySize: map[Side]int64{SideRes: 400 << 10}}
	panes, _, r, err := planFlow(nil, res, o.normalized(), themePalette(ThemeLight))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(r.BodyTruncated) != "[res]" || !strings.Contains(rowsText(panes[0].rows), "(400 KiB total)") {
		t.Fatalf("result %+v\n%s", r, rowsText(panes[0].rows))
	}
}

func TestClipLineStaysInBudgetAndKeepsCenter(t *testing.T) {
	line := strings.Repeat("x", 3000)
	rows := lineRows([]string{line}, 0, paneFocus{}, 100) // 30 segments
	for room := 2; room <= 32; room++ {
		for center := 0; center < len(rows); center++ {
			out := clipLine(rows, center, room)
			if len(out) > room {
				t.Fatalf("room %d center %d: %d rows", room, center, len(out))
			}
			kept, chars := false, 0
			for _, r := range out {
				if r.kind != rowMarker {
					chars += len(r.text)
					kept = kept || r.off == rows[center].off
					continue
				}
				var n int
				fmt.Sscanf(strings.TrimPrefix(r.text, "… "), "%d", &n)
				chars += n
			}
			if !kept {
				t.Fatalf("room %d center %d: center segment dropped", room, center)
			}
			if room > 2 && chars != 3000 {
				t.Fatalf("room %d center %d: shown+hidden chars %d", room, center, chars)
			}
		}
	}
}
