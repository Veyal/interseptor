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
	"strings"
	"sync"
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
	MaxLines int    // lines per pane before truncation (default 80)
	MaxCols  int    // characters per line before wrap/cut (default 100)
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
	lines []string
	bg    color.RGBA
}

// Render draws an Interseptor-branded HTTP viewer PNG from raw req/res bytes.
func Render(req, res []byte, opts Options) ([]byte, error) {
	side := opts.Side
	if side == "" {
		side = SideBoth
	}
	layout := opts.Layout
	if layout == "" {
		layout = LayoutVertical
	}
	theme := opts.Theme
	if theme == "" {
		theme = ThemeLight
	}
	pal := themePalette(theme)

	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	maxCols := opts.MaxCols
	if maxCols <= 0 {
		maxCols = defaultMaxCols
	}

	if opts.prettyOn() {
		req = prettyHTTP(req)
		res = prettyHTTP(res)
	}

	var panes []pane
	if side == SideBoth || side == SideReq {
		panes = append(panes, pane{
			label: "Request",
			lines: prepareLines(req, maxLines, maxCols),
			bg:    pal.reqTint,
		})
	}
	if side == SideBoth || side == SideRes {
		panes = append(panes, pane{
			label: "Response",
			lines: prepareLines(res, maxLines, maxCols),
			bg:    pal.resTint,
		})
	}
	if len(panes) == 0 {
		panes = append(panes, pane{label: "Empty", lines: []string{"(no content)"}, bg: pal.bg})
	}

	title := strings.TrimSpace(opts.Title)
	brand := "Interseptor"

	img, err := drawPreview(panes, layout, maxCols, title, brand, pal)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > MaxPNGBytes {
		opts2 := opts
		opts2.MaxLines = max(12, maxLines/2)
		if opts2.MaxLines >= maxLines {
			return nil, fmt.Errorf("preview PNG exceeds %d bytes", MaxPNGBytes)
		}
		return Render(req, res, opts2)
	}
	return out, nil
}

func drawPreview(panes []pane, layout Layout, maxCols int, title, brand string, pal palette) (*image.RGBA, error) {
	glyphMu.Lock()
	defer glyphMu.Unlock()
	face, err := monoFaceLocked()
	if err != nil {
		return nil, err
	}
	metrics := face.Metrics()
	lineH := (metrics.Ascent + metrics.Descent).Ceil() + lineGap
	charW := font.MeasureString(face, "M").Ceil()
	if charW < 1 {
		charW = 7
	}
	if layout == LayoutHorizontal && len(panes) >= 2 {
		return renderHorizontal(panes, face, metrics, lineH, charW, maxCols, title, brand, pal), nil
	}
	return renderVertical(panes, face, metrics, lineH, charW, maxCols, title, brand, pal), nil
}

func renderVertical(panes []pane, face font.Face, metrics font.Metrics, lineH, charW, maxCols int, title, brand string, pal palette) *image.RGBA {
	contentW := maxCols*charW + padX*2
	minW := font.MeasureString(face, brand+"  "+title).Ceil() + padX*2
	if contentW < minW {
		contentW = minW
	}
	if contentW < 480 {
		contentW = 480
	}

	height := titleBarH + padY
	for _, p := range panes {
		height += paneHeaderH + len(p.lines)*lineH + padY
	}
	height += padY

	img := image.NewRGBA(image.Rect(0, 0, contentW, height))
	drawChrome(img, contentW, face, metrics, title, brand, pal)

	d := &font.Drawer{Dst: img, Face: face}
	y := titleBarH + padY/2
	for _, p := range panes {
		paneTop := y
		paneH := paneHeaderH + len(p.lines)*lineH + padY/2
		drawPane(img, d, metrics, lineH, p, padX/2, paneTop, contentW-padX/2, paneH, pal)
		y = paneTop + paneH + padY/2
	}
	return img
}

func renderHorizontal(panes []pane, face font.Face, metrics font.Metrics, lineH, charW, maxCols int, title, brand string, pal palette) *image.RGBA {
	colW := maxCols*charW + padX*2
	if colW < 320 {
		colW = 320
	}
	contentW := colW*2 + paneGap + padX
	minW := font.MeasureString(face, brand+"  "+title).Ceil() + padX*2
	if contentW < minW {
		contentW = minW
	}

	maxLines := 0
	for _, p := range panes {
		if len(p.lines) > maxLines {
			maxLines = len(p.lines)
		}
	}
	paneH := paneHeaderH + maxLines*lineH + padY/2
	height := titleBarH + padY + paneH + padY

	img := image.NewRGBA(image.Rect(0, 0, contentW, height))
	drawChrome(img, contentW, face, metrics, title, brand, pal)

	d := &font.Drawer{Dst: img, Face: face}
	y := titleBarH + padY/2
	// Request on the left, Response on the right (Burp/HTTP-tool convention).
	left, right := panes[0], panes[1]
	if panes[0].label == "Response" && panes[1].label == "Request" {
		left, right = panes[1], panes[0]
	}
	x0 := padX / 2
	drawPane(img, d, metrics, lineH, left, x0, y, x0+colW, paneH, pal)
	x1 := x0 + colW + paneGap
	drawPane(img, d, metrics, lineH, right, x1, y, x1+colW, paneH, pal)
	return img
}

func drawChrome(img *image.RGBA, contentW int, face font.Face, metrics font.Metrics, title, brand string, pal palette) {
	draw.Draw(img, img.Bounds(), &image.Uniform{C: pal.bg}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, contentW, titleBarH), &image.Uniform{C: pal.titleBar}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, titleBarH-2, contentW, titleBarH), &image.Uniform{C: pal.accent}, image.Point{}, draw.Src)

	d := &font.Drawer{Dst: img, Src: image.NewUniform(pal.accent), Face: face}
	d.Dot = fixed.P(padX, titleBarH/2+metrics.Ascent.Ceil()/2-2)
	d.DrawString(brand)
	if title != "" {
		brandW := font.MeasureString(face, brand+"  ").Ceil()
		d.Src = image.NewUniform(pal.muted)
		d.Dot = fixed.P(padX+brandW, titleBarH/2+metrics.Ascent.Ceil()/2-2)
		d.DrawString(title)
	}
}

func drawPane(img *image.RGBA, d *font.Drawer, metrics font.Metrics, lineH int, p pane, x0, y0, x1, paneH int, pal palette) {
	draw.Draw(img, image.Rect(x0, y0, x1, y0+paneH), &image.Uniform{C: p.bg}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(x0, y0, x1, y0+paneHeaderH), &image.Uniform{C: pal.paneHdr}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(x0, y0, x0+3, y0+paneHeaderH), &image.Uniform{C: pal.accent}, image.Point{}, draw.Src)

	d.Src = image.NewUniform(pal.text)
	d.Dot = fixed.P(x0+padX/2+4, y0+paneHeaderH/2+metrics.Ascent.Ceil()/2-2)
	d.DrawString(p.label)

	textY := y0 + paneHeaderH + metrics.Ascent.Ceil()
	textX := x0 + padX/2
	for _, line := range p.lines {
		d.Src = image.NewUniform(pal.text)
		d.Dot = fixed.P(textX, textY)
		d.DrawString(line)
		textY += lineH
	}
	strokeRect(img, image.Rect(x0, y0, x1-1, y0+paneH-1), pal.border)
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
		var v any
		if err := json.Unmarshal(t, &v); err != nil {
			return body
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return body
		}
		return b
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

func prepareLines(raw []byte, maxLines, maxCols int) []string {
	if len(raw) == 0 {
		return []string{"(empty)"}
	}
	s := string(raw)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// Soft-wrap long lines before truncating line count.
	var wrapped []string
	for _, line := range strings.Split(s, "\n") {
		wrapped = append(wrapped, wrapLine(line, maxCols)...)
	}
	return truncateLines(wrapped, maxLines)
}

func wrapLine(line string, maxCols int) []string {
	if maxCols <= 0 || len(line) <= maxCols {
		return []string{line}
	}
	var out []string
	for len(line) > maxCols {
		out = append(out, line[:maxCols])
		line = line[maxCols:]
	}
	if line != "" || len(out) == 0 {
		out = append(out, line)
	}
	return out
}

func truncateLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	keep := maxLines - 1
	if keep < 1 {
		keep = 1
	}
	out := append([]string{}, lines[:keep]...)
	out = append(out, "… [truncated]")
	return out
}
