// Package preview renders Interseptor-styled HTTP request/response PNG previews
// for findings evidence and report screenshots (pure Go, no cgo).
package preview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"golang.org/x/image/font/gofont/gomono"
)

// Side selects which HTTP message(s) to include in the preview.
type Side string

const (
	SideBoth Side = "both"
	SideReq  Side = "req"
	SideRes  Side = "res"
)

// Layout selects stacked (vertical) or side-by-side (horizontal) panes.
type Layout string

const (
	LayoutVertical   Layout = "vertical"
	LayoutHorizontal Layout = "horizontal"
)

// Theme selects color palette.
type Theme string

const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

// MaxPNGBytes is the hard cap for generated PNGs (matches store image upload limit).
const MaxPNGBytes = 5 << 20

const (
	defaultMaxLines = 80
	defaultMaxCols  = 100
	prettyMaxBody   = 256 << 10
	padX            = 16
	padY            = 12
	titleBarH       = 36
	paneHeaderH     = 28
	lineGap         = 2
	paneGap         = 8
)

// Options controls layout and truncation for Render.
type Options struct {
	Side     Side
	Layout   Layout // vertical (default) | horizontal
	Theme    Theme  // light (default) | dark
	Pretty   *bool  // indent JSON/XML bodies; nil defaults to true
	Title    string // subtitle under the product chrome (method host path → status)
	MaxLines int    // rows per pane before truncation (default 80)
	MaxCols  int    // characters per line before wrap/cut (default 100)

	// Focus and Lines switch a pane from "top" mode (first MaxLines rows) to
	// "focus" mode (first line, collapsed headers, ±Context lines around each
	// match or range, highlighted). Empty means top mode.
	Focus   []FocusSpec
	Lines   []LineRange
	Context *int   // logical lines around each match/range; nil = 6, clamped 0..40
	Caption string // short note drawn under the title (≤120 chars)
	// Mask redacts the raw message before rendering (credential headers). A
	// focus text found only in the unmasked text is reported as MaskedMatch.
	Mask func(string) string
	// Deadline aborts the render with ErrRenderTimeout once passed.
	Deadline time.Time
	// BodyCap cuts each displayed body to this many bytes after masking and
	// pretty-printing (0 keeps it whole), so line numbers stay those of the
	// formatted body; the pane then ends with a truncation notice row.
	BodyCap int
	// BodySize is each side's full captured body size. A side whose size is
	// larger than the body passed in was already cut by the caller.
	BodySize map[Side]int64
	// Notices are one-line notes drawn as bands under the caption, e.g. which
	// body decoding was applied. Callers word them; blank ones are skipped.
	Notices []string
}

// Bool returns a *bool for Options.Pretty literals.
func Bool(v bool) *bool { return &v }

func (o Options) prettyOn() bool {
	if o.Pretty == nil {
		return true
	}
	return *o.Pretty
}

// NormalizeSide maps user input to a Side; unknown/empty → SideBoth.
func NormalizeSide(s string) Side {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "req", "request":
		return SideReq
	case "res", "resp", "response":
		return SideRes
	default:
		return SideBoth
	}
}

// NormalizeLayout maps user input. Empty and unknown values are vertical
// (request above response). Explicit side-by-side aliases stay horizontal.
func NormalizeLayout(s string) Layout {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "horizontal", "horiz", "side", "split", "row", "columns":
		return LayoutHorizontal
	default:
		return LayoutVertical
	}
}

// NormalizeTheme maps user input; unknown/empty → ThemeLight.
func NormalizeTheme(s string) Theme {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dark":
		return ThemeDark
	default:
		return ThemeLight
	}
}

// ParseBool parses common truthy/falsy query/body values; empty uses def.
func ParseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off", "n":
		return false
	default:
		return def
	}
}

type palette struct {
	bg, titleBar, paneHdr, border, accent, text, muted, reqTint, resTint color.RGBA
	// Focus mode: highlighter box and its ink, the gutter bar on matched rows,
	// the gap-marker band and the not-found notice.
	hl, hlText, hlBar, marker, notice color.RGBA
}

func themePalette(t Theme) palette {
	if t == ThemeLight {
		return palette{
			bg:       color.RGBA{R: 0xf4, G: 0xf6, B: 0xfa, A: 0xff},
			titleBar: color.RGBA{R: 0xe8, G: 0xec, B: 0xf2, A: 0xff},
			paneHdr:  color.RGBA{R: 0xde, G: 0xe4, B: 0xec, A: 0xff},
			border:   color.RGBA{R: 0xb8, G: 0xc2, B: 0xd0, A: 0xff},
			accent:   color.RGBA{R: 0x1a, G: 0x73, B: 0xb8, A: 0xff},
			text:     color.RGBA{R: 0x1a, G: 0x1f, B: 0x2a, A: 0xff},
			muted:    color.RGBA{R: 0x5a, G: 0x66, B: 0x7a, A: 0xff},
			reqTint:  color.RGBA{R: 0xea, G: 0xf6, B: 0xef, A: 0xff},
			resTint:  color.RGBA{R: 0xe8, G: 0xf0, B: 0xfa, A: 0xff},
			hl:       color.RGBA{R: 0xff, G: 0xe4, B: 0x5c, A: 0xff},
			hlText:   color.RGBA{R: 0x1a, G: 0x1f, B: 0x2a, A: 0xff},
			hlBar:    color.RGBA{R: 0xc7, G: 0x78, B: 0x00, A: 0xff},
			marker:   color.RGBA{R: 0xe3, G: 0xe8, B: 0xef, A: 0xff},
			notice:   color.RGBA{R: 0x8a, G: 0x4b, B: 0x00, A: 0xff},
		}
	}
	return palette{
		bg:       color.RGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff},
		titleBar: color.RGBA{R: 0x1e, G: 0x22, B: 0x2c, A: 0xff},
		paneHdr:  color.RGBA{R: 0x25, G: 0x2a, B: 0x36, A: 0xff},
		border:   color.RGBA{R: 0x3a, G: 0x42, B: 0x55, A: 0xff},
		accent:   color.RGBA{R: 0x5b, G: 0x9f, B: 0xd4, A: 0xff},
		text:     color.RGBA{R: 0xe6, G: 0xea, B: 0xf0, A: 0xff},
		muted:    color.RGBA{R: 0x8b, G: 0x95, B: 0xa8, A: 0xff},
		reqTint:  color.RGBA{R: 0x1a, G: 0x2a, B: 0x22, A: 0xff},
		resTint:  color.RGBA{R: 0x1a, G: 0x22, B: 0x2e, A: 0xff},
		hl:       color.RGBA{R: 0xff, G: 0xd5, B: 0x4f, A: 0xff},
		hlText:   color.RGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff},
		hlBar:    color.RGBA{R: 0xff, G: 0xd5, B: 0x4f, A: 0xff},
		marker:   color.RGBA{R: 0x24, G: 0x29, B: 0x35, A: 0xff},
		notice:   color.RGBA{R: 0xff, G: 0xb7, B: 0x4d, A: 0xff},
	}
}

// glyphMu serializes use of the shared sfnt font and face. golang.org/x/image
// font caches are not safe for concurrent Glyph or MeasureString calls, which
// panic when several preview.png requests render at once.
var (
	glyphMu   sync.Mutex
	faceCache font.Face
)

func monoFace() (font.Face, error) {
	glyphMu.Lock()
	defer glyphMu.Unlock()
	return monoFaceLocked()
}

func monoFaceLocked() (font.Face, error) {
	if faceCache != nil {
		return faceCache, nil
	}
	f, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    12,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	faceCache = face
	return face, nil
}

type pane struct {
	label string
	rows  []planRow
	bg    color.RGBA
}

// band is a full-width note under the title bar: the caption or a notice.
type band struct {
	text   string
	notice bool
}

// paneSource is one side's displayed lines and what it should center on.
type paneSource struct {
	side      Side
	label     string
	lines     []string
	focus     paneFocus
	searched  bool // a focus spec or line range applied to this side
	truncated bool // the body was cut for the image
	passed    int  // body bytes passed in (before the cut)
	bg        color.RGBA
}

// Render draws an Interseptor-branded HTTP viewer PNG from raw req/res bytes.
func Render(req, res []byte, opts Options) ([]byte, error) {
	r, err := RenderFlow(req, res, opts)
	return r.PNG, err
}

func (o Options) normalized() Options {
	if o.Side == "" {
		o.Side = SideBoth
	}
	if o.Layout == "" {
		o.Layout = LayoutVertical
	}
	if o.Theme == "" {
		o.Theme = ThemeLight
	}
	if o.MaxLines <= 0 {
		o.MaxLines = defaultMaxLines
	}
	if o.MaxCols <= 0 {
		o.MaxCols = defaultMaxCols
	}
	return o
}

func expired(deadline time.Time) bool { return !deadline.IsZero() && time.Now().After(deadline) }

// RenderFlow is Render plus the focus outcome: mode, matches per side, focus
// texts or ranges that matched nothing, and whether one only hit a masked value.
func RenderFlow(req, res []byte, opts Options) (RenderResult, error) {
	o := opts.normalized()
	if err := ValidateFocus(o); err != nil {
		return RenderResult{}, err
	}
	if expired(o.Deadline) {
		return RenderResult{}, ErrRenderTimeout
	}
	pal := themePalette(o.Theme)
	panes, bands, result, err := planFlow(req, res, o, pal)
	if err != nil {
		return RenderResult{}, err
	}
	if expired(o.Deadline) {
		return RenderResult{}, ErrRenderTimeout
	}
	img, err := drawPreview(panes, o.Layout, o.MaxCols, strings.TrimSpace(o.Title), "Interseptor", bands, pal)
	if err != nil {
		return RenderResult{}, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return RenderResult{}, err
	}
	if buf.Len() > MaxPNGBytes {
		smaller := opts
		smaller.MaxLines = max(12, o.MaxLines/2)
		if smaller.MaxLines >= o.MaxLines {
			return RenderResult{}, fmt.Errorf("preview PNG exceeds %d bytes", MaxPNGBytes)
		}
		return RenderFlow(req, res, smaller)
	}
	result.PNG = buf.Bytes()
	return result, nil
}

// displayText is what a pane shows: masked, pretty-printed, then cut to
// BodyCap. It reports whether the body is incomplete in the image.
func displayText(raw []byte, o Options, side Side, mask bool) ([]byte, bool) {
	passed := len(splitBody(raw))
	if mask && o.Mask != nil && len(raw) > 0 {
		raw = []byte(o.Mask(string(raw)))
	}
	if o.prettyOn() {
		raw = prettyHTTP(raw)
	}
	cut := o.BodySize[side] > int64(passed)
	if o.BodyCap > 0 {
		body := splitBody(raw)
		if len(body) > o.BodyCap {
			raw = raw[:len(raw)-len(body)+cutBody(body, o.BodyCap)]
			cut = true
		}
	}
	return raw, cut
}

// splitBody returns the body after the first blank line (nil without one).
func splitBody(raw []byte) []byte {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[i+4:]
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[i+2:]
	}
	return nil
}

// cutBody picks a cut length ≤ limit: after the last newline when that keeps
// most of the budget, else on a UTF-8 boundary.
func cutBody(body []byte, limit int) int {
	if i := bytes.LastIndexByte(body[:limit], '\n'); i >= limit/2 {
		return i
	}
	n := limit
	for n > 0 && !utf8.RuneStart(body[n]) {
		n--
	}
	return n
}

// truncationNotice is the last row of a pane whose body was cut.
func truncationNotice(o Options, side Side, passed int) planRow {
	total := o.BodySize[side]
	if total <= 0 {
		total = int64(passed)
	}
	if o.BodyCap <= 0 {
		return markerRow("Body truncated for the image (%s total)", kib(total))
	}
	return markerRow("Body truncated at %s for the image (%s total)", kib(int64(o.BodyCap)), kib(total))
}

func kib(n int64) string { return fmt.Sprintf("%d KiB", (n+1023)/1024) }

// planFlow matches focus specs, picks each pane's mode and plans its rows.
func planFlow(req, res []byte, o Options, pal palette) ([]pane, []band, RenderResult, error) {
	matchers, err := compileFocus(o.Focus)
	if err != nil {
		return nil, nil, RenderResult{}, err
	}
	result := RenderResult{Mode: ModeTop, Matches: map[Side]int{}}
	sources := flowSources(req, res, o, pal)
	specHits := make([]int, len(o.Focus))
	rangeFound := make([]bool, len(o.Lines))
	for i := range sources {
		s := &sources[i]
		hits, counts := findHits(s.lines, matchers, o.Focus, s.side)
		s.focus.hits = hits
		for k, c := range counts {
			specHits[k] += c
			result.Matches[s.side] += c
			s.searched = s.searched || appliesTo(o.Focus[k].Side, s.side)
		}
		for k, r := range o.Lines {
			if !appliesTo(r.Side, s.side) {
				continue
			}
			s.searched = true
			if r.From <= len(s.lines) {
				rangeFound[k] = true
				s.focus.ranges = append(s.focus.ranges, [2]int{r.From - 1, min(r.To, len(s.lines)) - 1})
			}
		}
		if s.focus.anchored() {
			result.Mode = ModeFocus
		}
	}
	var masked []bool
	if o.Mask != nil {
		masked = matchesUnmasked(req, res, o, matchers, specHits)
	}
	collectNotFound(o, specHits, rangeFound, masked, &result)
	panes := make([]pane, 0, len(sources))
	for _, s := range sources {
		rows := planSource(s, o, result.Mode)
		if result.Matches[s.side] > 0 && !drawnMatches(rows) {
			result.NotShown = append(result.NotShown, s.side)
		}
		if s.truncated {
			result.BodyTruncated = append(result.BodyTruncated, s.side)
			rows = append(rows, truncationNotice(o, s.side, s.passed))
		}
		panes = append(panes, pane{label: s.label, rows: rows, bg: s.bg})
	}
	if len(panes) == 0 {
		panes = append(panes, pane{label: "Empty", rows: []planRow{{kind: rowMarker, text: "(no content)"}}, bg: pal.bg})
	}
	return panes, flowBands(o.Caption, result, o.BodyCap, o.Notices), result, nil
}

func flowSources(req, res []byte, o Options, pal palette) []paneSource {
	var out []paneSource
	add := func(side Side, label string, raw []byte, bg color.RGBA) {
		text, cut := displayText(raw, o, side, true)
		out = append(out, paneSource{side: side, label: label, lines: logicalLines(text), truncated: cut, passed: len(splitBody(raw)), bg: bg})
	}
	if o.Side == SideBoth || o.Side == SideReq {
		add(SideReq, "Request", req, pal.reqTint)
	}
	if o.Side == SideBoth || o.Side == SideRes {
		add(SideRes, "Response", res, pal.resTint)
	}
	return out
}

// collectNotFound fills result.NotFound. A spec that only matched a masked
// value sets MaskedMatch instead and is never echoed.
func collectNotFound(o Options, specHits []int, rangeFound []bool, masked []bool, result *RenderResult) {
	for k, n := range specHits {
		switch {
		case n > 0:
		case k < len(masked) && masked[k]:
			result.MaskedMatch = true
		default:
			result.NotFound = append(result.NotFound, focusLabel(o.Focus[k]))
		}
	}
	for k, ok := range rangeFound {
		if !ok {
			result.NotFound = append(result.NotFound, o.Lines[k].String())
		}
	}
}

func focusLabel(s FocusSpec) string {
	if s.Regex {
		return "/" + s.Text + "/"
	}
	return strings.TrimSpace(s.Text)
}

// matchesUnmasked marks each spec that found nothing in the displayed text
// but occurs in the unmasked text, i.e. only inside a masked credential
// value. Nothing is unmasked.
func matchesUnmasked(req, res []byte, o Options, matchers []*regexp.Regexp, specHits []int) []bool {
	out := make([]bool, len(specHits))
	for _, src := range []struct {
		side Side
		raw  []byte
	}{{SideReq, req}, {SideRes, res}} {
		if o.Side != SideBoth && o.Side != src.side {
			continue
		}
		text, _ := displayText(src.raw, o, src.side, false)
		lines := logicalLines(text)
		for k, n := range specHits {
			if n > 0 || out[k] || !appliesTo(o.Focus[k].Side, src.side) {
				continue
			}
			for _, l := range lines {
				if matchers[k].MatchString(l) {
					out[k] = true
					break
				}
			}
		}
	}
	return out
}

// planSource picks a pane's layout: focus windows when it has anchors, the
// compact form when it was searched without a hit, top mode otherwise.
// A searched pane this short is shown whole: collapsing it would save less
// than its markers cost and hide the request a reviewer needs.
const compactMinRows = 10

func planSource(s paneSource, o Options, mode string) []planRow {
	if mode == ModeFocus && s.focus.anchored() {
		return planFocus(s.lines, s.focus, o.contextLines(), o.MaxLines, o.MaxCols)
	}
	top := planTop(s.lines, s.focus, o.MaxLines, o.MaxCols)
	if mode == ModeFocus && s.searched && len(top) > compactMinRows {
		return planFocus(s.lines, s.focus, o.contextLines(), o.MaxLines, o.MaxCols)
	}
	return top
}

func flowBands(caption string, r RenderResult, bodyCap int, notices []string) []band {
	var out []band
	if c := cleanCaption(caption); c != "" {
		out = append(out, band{text: c})
	}
	for _, n := range notices {
		if n = cleanCaption(n); n != "" {
			out = append(out, band{text: clipText(n, 160), notice: true})
		}
	}
	var parts []string
	if r.MaskedMatch {
		parts = append(parts, "Focus matched a masked value (hidden)")
	}
	if len(r.NotFound) > 0 {
		lead := "Focus text not found: "
		if len(r.BodyTruncated) > 0 && bodyCap > 0 {
			lead = fmt.Sprintf("Focus text not found in the first %s of the body: ", kib(int64(bodyCap)))
		}
		if r.MaskedMatch {
			lead = strings.ToLower(lead[:1]) + lead[1:]
		}
		parts = append(parts, lead+strings.Join(quoteAll(r.NotFound), ", "))
	}
	if len(parts) > 0 {
		out = append(out, band{text: clipText(strings.Join(parts, "; "), 160), notice: true})
	}
	return out
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = "“" + s + "”"
	}
	return out
}

// cleanCaption flattens control characters and caps the caption length.
func cleanCaption(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return clipText(strings.Join(strings.Fields(s), " "), MaxCaptionLen)
}

func clipText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// drawMetrics carries the font measurements shared by every pane.
type drawMetrics struct {
	face    font.Face
	metrics font.Metrics
	lineH   int
	charW   int
	gutterW int // line-number gutter including the focus bar
	numCols int
}

const (
	gutterBarW = 3
	bandPadY   = 8
)

func gutterCols(panes []pane) int {
	maxNo := 0
	for _, p := range panes {
		for _, r := range p.rows {
			maxNo = max(maxNo, r.lineNo)
		}
	}
	return max(3, len(fmt.Sprint(maxNo)))
}

func drawPreview(panes []pane, layout Layout, maxCols int, title, brand string, bands []band, pal palette) (*image.RGBA, error) {
	glyphMu.Lock()
	defer glyphMu.Unlock()
	face, err := monoFaceLocked()
	if err != nil {
		return nil, err
	}
	m := drawMetrics{face: face, metrics: face.Metrics()}
	m.lineH = (m.metrics.Ascent + m.metrics.Descent).Ceil() + lineGap
	m.charW = font.MeasureString(face, "M").Ceil()
	if m.charW < 1 {
		m.charW = 7
	}
	m.numCols = gutterCols(panes)
	m.gutterW = gutterBarW + 6 + m.numCols*m.charW + 8
	if layout == LayoutHorizontal && len(panes) >= 2 {
		return renderHorizontal(panes, m, maxCols, title, brand, bands, pal), nil
	}
	return renderVertical(panes, m, maxCols, title, brand, bands, pal), nil
}

func chromeHeight(m drawMetrics, bands []band) int {
	return titleBarH + len(bands)*(m.lineH+bandPadY)
}

func chromeMinWidth(m drawMetrics, title, brand string, bands []band) int {
	w := font.MeasureString(m.face, brand+"  "+title).Ceil() + padX*2
	for _, b := range bands {
		w = max(w, font.MeasureString(m.face, b.text).Ceil()+padX*2+8)
	}
	return w
}

func renderVertical(panes []pane, m drawMetrics, maxCols int, title, brand string, bands []band, pal palette) *image.RGBA {
	contentW := max(max(m.gutterW+maxCols*m.charW+padX*2, chromeMinWidth(m, title, brand, bands)), 480)
	chromeH := chromeHeight(m, bands)
	height := chromeH + padY
	for _, p := range panes {
		height += paneHeaderH + len(p.rows)*m.lineH + padY
	}
	height += padY

	img := image.NewRGBA(image.Rect(0, 0, contentW, height))
	drawChrome(img, contentW, m, title, brand, bands, pal)

	d := &font.Drawer{Dst: img, Face: m.face}
	y := chromeH + padY/2
	for _, p := range panes {
		paneH := paneHeaderH + len(p.rows)*m.lineH + padY/2
		drawPane(img, d, m, p, padX/2, y, contentW-padX/2, paneH, pal)
		y += paneH + padY/2
	}
	return img
}

func renderHorizontal(panes []pane, m drawMetrics, maxCols int, title, brand string, bands []band, pal palette) *image.RGBA {
	colW := max(m.gutterW+maxCols*m.charW+padX*2, 320)
	contentW := max(colW*2+paneGap+padX, chromeMinWidth(m, title, brand, bands))
	rows := 0
	for _, p := range panes {
		rows = max(rows, len(p.rows))
	}
	paneH := paneHeaderH + rows*m.lineH + padY/2
	chromeH := chromeHeight(m, bands)
	height := chromeH + padY + paneH + padY

	img := image.NewRGBA(image.Rect(0, 0, contentW, height))
	drawChrome(img, contentW, m, title, brand, bands, pal)

	d := &font.Drawer{Dst: img, Face: m.face}
	y := chromeH + padY/2
	// Request on the left, Response on the right (Burp/HTTP-tool convention).
	left, right := panes[0], panes[1]
	if panes[0].label == "Response" && panes[1].label == "Request" {
		left, right = panes[1], panes[0]
	}
	x0 := padX / 2
	drawPane(img, d, m, left, x0, y, x0+colW, paneH, pal)
	x1 := x0 + colW + paneGap
	drawPane(img, d, m, right, x1, y, x1+colW, paneH, pal)
	return img
}

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawChrome(img *image.RGBA, contentW int, m drawMetrics, title, brand string, bands []band, pal palette) {
	fillRect(img, img.Bounds(), pal.bg)
	fillRect(img, image.Rect(0, 0, contentW, titleBarH), pal.titleBar)
	fillRect(img, image.Rect(0, titleBarH-2, contentW, titleBarH), pal.accent)

	asc := m.metrics.Ascent.Ceil()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(pal.accent), Face: m.face}
	d.Dot = fixed.P(padX, titleBarH/2+asc/2-2)
	d.DrawString(brand)
	if title != "" {
		brandW := font.MeasureString(m.face, brand+"  ").Ceil()
		d.Src = image.NewUniform(pal.muted)
		d.Dot = fixed.P(padX+brandW, titleBarH/2+asc/2-2)
		d.DrawString(title)
	}
	y := titleBarH
	for _, b := range bands {
		h := m.lineH + bandPadY
		bar, fg := pal.accent, pal.text
		if b.notice {
			bar, fg = pal.notice, pal.notice
		}
		fillRect(img, image.Rect(padX/2, y+bandPadY/2, padX/2+gutterBarW, y+h), bar)
		d.Src = image.NewUniform(fg)
		d.Dot = fixed.P(padX+4, y+bandPadY/2+asc)
		d.DrawString(b.text)
		y += h
	}
}

func drawPane(img *image.RGBA, d *font.Drawer, m drawMetrics, p pane, x0, y0, x1, paneH int, pal palette) {
	fillRect(img, image.Rect(x0, y0, x1, y0+paneH), p.bg)
	fillRect(img, image.Rect(x0, y0, x1, y0+paneHeaderH), pal.paneHdr)
	fillRect(img, image.Rect(x0, y0, x0+3, y0+paneHeaderH), pal.accent)

	d.Src = image.NewUniform(pal.text)
	d.Dot = fixed.P(x0+padX/2+4, y0+paneHeaderH/2+m.metrics.Ascent.Ceil()/2-2)
	d.DrawString(p.label)

	top := y0 + paneHeaderH
	for _, r := range p.rows {
		drawRow(img, d, m, r, x0, x1, top, pal)
		top += m.lineH
	}
	strokeRect(img, image.Rect(x0, y0, x1-1, y0+paneH-1), pal.border)
}

// drawRow paints one planned row: gutter bar and number, then the text with
// highlighter boxes behind matched spans (redrawn in the highlight ink).
func drawRow(img *image.RGBA, d *font.Drawer, m drawMetrics, r planRow, x0, x1, top int, pal palette) {
	base := top + m.metrics.Ascent.Ceil()
	barX := x0 + 4
	textX := x0 + m.gutterW
	switch {
	case r.match():
		fillRect(img, image.Rect(barX, top, barX+gutterBarW, top+m.lineH), pal.hlBar)
	case r.inRange:
		fillRect(img, image.Rect(barX, top, barX+gutterBarW, top+m.lineH), pal.accent)
	}
	if r.kind == rowMarker {
		fillRect(img, image.Rect(x0+1, top, x1-1, top+m.lineH), pal.marker)
		d.Src = image.NewUniform(pal.muted)
		d.Dot = fixed.P(textX, base)
		d.DrawString(r.text)
		return
	}
	if r.kind == rowLine {
		num := fmt.Sprint(r.lineNo)
		d.Src = image.NewUniform(pal.muted)
		d.Dot = fixed.P(textX-8-len(num)*m.charW, base)
		d.DrawString(num)
	}
	d.Src = image.NewUniform(pal.text)
	d.Dot = fixed.P(textX, base)
	d.DrawString(r.text)
	for _, h := range r.hl {
		sx := fixed.I(textX) + font.MeasureString(m.face, r.text[:h.start])
		ex := sx + font.MeasureString(m.face, r.text[h.start:h.end])
		fillRect(img, image.Rect(sx.Floor(), top, ex.Ceil(), top+m.lineH-1), pal.hl)
		d.Src = image.NewUniform(pal.hlText)
		d.Dot = fixed.Point26_6{X: sx, Y: fixed.I(base)}
		d.DrawString(r.text[h.start:h.end])
	}
}

func strokeRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for x := r.Min.X; x <= r.Max.X; x++ {
		img.Set(x, r.Min.Y, c)
		img.Set(x, r.Max.Y, c)
	}
	for y := r.Min.Y; y <= r.Max.Y; y++ {
		img.Set(r.Min.X, y, c)
		img.Set(r.Max.X, y, c)
	}
}

// prettyHTTP indents JSON/XML bodies while leaving the header block intact.
func prettyHTTP(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	sep := []byte("\r\n\r\n")
	splitLen := 4
	i := bytes.Index(raw, sep)
	if i < 0 {
		sep = []byte("\n\n")
		splitLen = 2
		i = bytes.Index(raw, sep)
	}
	if i < 0 {
		return raw
	}
	head, body := raw[:i], raw[i+splitLen:]
	if len(body) == 0 || len(body) > prettyMaxBody {
		return raw
	}
	pretty := beautifyBody(body)
	if bytes.Equal(pretty, body) {
		return raw
	}
	out := make([]byte, 0, len(head)+2+len(pretty))
	out = append(out, head...)
	out = append(out, '\n', '\n')
	out = append(out, pretty...)
	return out
}

func beautifyBody(body []byte) []byte {
	t := bytes.TrimLeft(body, " \t\r\n")
	if len(t) == 0 {
		return body
	}
	if t[0] == '{' || t[0] == '[' {
		// json.Indent keeps key order, number text and string escapes as
		// captured and never HTML-escapes. The UI inspector re-serializes
		// with JSON.parse/stringify instead, which decodes \u003c-style
		// escapes, drops duplicate keys and moves integer-like keys first,
		// so line numbers agree only for ordinary JSON. Focus text matching
		// tolerates the escape difference (see focusRegexp).
		var compact, out bytes.Buffer
		if err := json.Compact(&compact, t); err != nil {
			return body
		}
		if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
			return body
		}
		return out.Bytes()
	}
	if t[0] == '<' {
		return beautifyMarkup(t)
	}
	return body
}

// beautifyMarkup applies a light XML/HTML indent (same idea as the UI prettify).
func beautifyMarkup(body []byte) []byte {
	s := string(body)
	s = strings.ReplaceAll(s, "><", ">\n<")
	var out strings.Builder
	depth := 0
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "</") {
			depth = max(0, depth-1)
		}
		out.WriteString(strings.Repeat("  ", depth))
		out.WriteString(ln)
		out.WriteByte('\n')
		if isOpenMarkupTag(ln) {
			depth++
		}
	}
	return []byte(strings.TrimRight(out.String(), "\n"))
}

func isOpenMarkupTag(ln string) bool {
	if !strings.HasPrefix(ln, "<") || strings.HasPrefix(ln, "</") || strings.HasPrefix(ln, "<?") || strings.HasPrefix(ln, "<!") {
		return false
	}
	if strings.HasSuffix(ln, "/>") {
		return false
	}
	// Self-closing void-ish single tags ending with >
	lower := strings.ToLower(ln)
	for _, void := range []string{"<area", "<base", "<br", "<col", "<embed", "<hr", "<img", "<input", "<link", "<meta", "<param", "<source", "<track", "<wbr"} {
		if strings.HasPrefix(lower, void) {
			return false
		}
	}
	return strings.HasSuffix(ln, ">")
}
