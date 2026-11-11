package preview

import (
	"bytes"
	"encoding/json"
	"image/png"
	"strings"
	"testing"
)

// A small, deeply nested JSON body must not expand quadratically when it is
// pretty-printed: json.Indent writes 2*depth spaces per line, so 16 KiB of
// brackets used to become 32 MiB (and a 256 KiB body several GiB).
func TestBeautifyBodyBoundsDeeplyNestedJSON(t *testing.T) {
	chunk := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	body := []byte("[" + strings.Repeat(chunk+",", 4) + "1]")
	out := beautifyBody(body)
	if len(out) > prettyMaxOut {
		t.Fatalf("pretty-printed %d bytes into %d bytes (cap %d)", len(body), len(out), prettyMaxOut)
	}
}

// The same holds for the markup indenter: every unclosed tag used to deepen
// the indent of all following lines without bound.
func TestBeautifyMarkupBoundsIndentDepth(t *testing.T) {
	body := []byte(strings.Repeat("<a>", 5000))
	out := beautifyBody(body)
	if max := len(body) + 5000*(2*maxMarkupIndent+1); len(out) > max {
		t.Fatalf("indented %d bytes of markup into %d bytes (want <= %d)", len(body), len(out), max)
	}
	if !bytes.HasPrefix(out, []byte("<a>\n  <a>\n    <a>\n")) {
		t.Fatalf("shallow markup must still indent: %q", out[:40])
	}
}

// Ordinary JSON keeps the exact json.Indent rendering (line numbers must match).
func TestBeautifyBodyKeepsOrdinaryJSONIndent(t *testing.T) {
	for _, body := range []string{
		`{"a":[1,2,{"b":"x,y:{}[]\"q\\"}],"c":{},"d":[],"e":null}`,
		`[[],[[]],{"k":{"k":{"k":[true,false]}}}]`,
		` {"spaced" : [ 1 , 2 ] } `,
	} {
		var want bytes.Buffer
		if err := json.Indent(&want, []byte(strings.TrimSpace(body)), "", "  "); err != nil {
			t.Fatal(err)
		}
		if got := beautifyBody([]byte(body)); !bytes.Equal(got, want.Bytes()) {
			t.Errorf("beautifyBody(%s)\n got %q\nwant %q", body, got, want.Bytes())
		}
		var compact bytes.Buffer
		_ = json.Compact(&compact, []byte(body))
		if got := indentedJSONSize(compact.Bytes()); got != want.Len() {
			t.Errorf("indentedJSONSize(%s) = %d, json.Indent wrote %d", body, got, want.Len())
		}
	}
}

// The title is the flow's method, host and path. A long captured path must not
// set the image width: it used to widen the PNG by ~7px per character.
func TestRenderFlowBoundsWidthForLongTitle(t *testing.T) {
	req := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	res := []byte("HTTP/1.1 200 OK\r\n\r\nhi")
	short, err := RenderFlow(req, res, Options{Title: "GET example.com/ → 200"})
	if err != nil {
		t.Fatal(err)
	}
	long, err := RenderFlow(req, res, Options{Title: "GET example.com/" + strings.Repeat("a", 4000) + " → 200"})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := png.DecodeConfig(bytes.NewReader(short.PNG))
	if err != nil {
		t.Fatal(err)
	}
	lc, err := png.DecodeConfig(bytes.NewReader(long.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if lc.Width > 3*sc.Width {
		t.Fatalf("a long title widened the image from %dpx to %dpx", sc.Width, lc.Width)
	}
}

func TestClipTitleKeepsBothEnds(t *testing.T) {
	title := "GET example.com/" + strings.Repeat("a", 4000) + " → 200"
	got := clipTitle(title)
	if n := len([]rune(got)); n > maxTitleRunes {
		t.Fatalf("clipped title is %d runes, want <= %d", n, maxTitleRunes)
	}
	if !strings.HasPrefix(got, "GET example.com/") || !strings.HasSuffix(got, " → 200") || !strings.Contains(got, "…") {
		t.Fatalf("clipped title lost an end: %q", got)
	}
	if short := "GET example.com/ → 200"; clipTitle(short) != short {
		t.Fatalf("short title changed: %q", clipTitle(short))
	}
}
