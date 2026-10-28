package preview

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FocusSpec asks a flow preview to center on text instead of the first lines.
// Text is a case-insensitive literal (whitespace after ':' and ',' is flexible,
// so a raw `"isAdmin":true` also matches the pretty-printed `"isAdmin": true`),
// or an RE2 pattern when Regex is set. Matching runs per logical line on the
// displayed text: after pretty-printing and header masking.
type FocusSpec struct {
	Side  Side // req | res | both (empty means both)
	Text  string
	Regex bool
}

// LineRange keeps logical lines From..To (1-based, inclusive) of one side.
// Logical lines are counted after pretty-printing and masking but before soft
// wrap, so they match the line numbers drawn in the gutter.
type LineRange struct {
	Side     Side // req | res | both (empty means both)
	From, To int
}

// RenderResult is a flow preview plus what the focus request matched.
type RenderResult struct {
	PNG         []byte
	Mode        string       // "top" or "focus"
	Matches     map[Side]int // highlighted occurrences per rendered side
	NotFound    []string     // focus texts / line ranges that matched nothing
	MaskedMatch bool         // a focus text occurs only in a masked header value (never echoed)
	// NotShown lists sides with matches none of which could be drawn (row
	// budget); callers must not claim a visible match there.
	NotShown []Side
	// BodyTruncated lists sides whose body was cut for the image (BodyCap).
	BodyTruncated []Side
}

// Focus bounds keep a request from turning the renderer into a search engine.
const (
	DefaultFocusContext = 6
	MaxFocusContext     = 40
	MaxFocusSpecs       = 8
	MaxFocusTextLen     = 200
	MaxLineRanges       = 8
	MaxCaptionLen       = 120
	maxFocusHits        = 4096 // highlighted spans kept per pane
)

// Focus modes reported in RenderResult.Mode.
const (
	ModeTop   = "top"
	ModeFocus = "focus"
)

// ErrInvalidFocus wraps every focus/lines validation error (a 400 for callers).
var ErrInvalidFocus = errors.New("invalid focus")

// Int returns a *int for Options.Context literals.
func Int(v int) *int { return &v }

func focusErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFocus, fmt.Sprintf(format, a...))
}

// contextLines resolves Options.Context: nil means the default, clamped 0..40.
func (o Options) contextLines() int {
	if o.Context == nil {
		return DefaultFocusContext
	}
	return min(max(*o.Context, 0), MaxFocusContext)
}

// focusRequested reports whether the caller asked for focus mode at all.
func (o Options) focusRequested() bool { return len(o.Focus) > 0 || len(o.Lines) > 0 }

// ValidateFocus checks focus specs and line ranges without rendering.
func ValidateFocus(o Options) error {
	_, err := compileFocus(o.Focus)
	if err != nil {
		return err
	}
	if len(o.Lines) > MaxLineRanges {
		return focusErr("at most %d line ranges", MaxLineRanges)
	}
	for _, r := range o.Lines {
		if r.From < 1 || r.To < r.From {
			return focusErr("line range %d-%d must be 1-based with from <= to", r.From, r.To)
		}
	}
	return nil
}

// compileFocus turns specs into case-insensitive matchers.
func compileFocus(specs []FocusSpec) ([]*regexp.Regexp, error) {
	if len(specs) > MaxFocusSpecs {
		return nil, focusErr("at most %d focus texts", MaxFocusSpecs)
	}
	out := make([]*regexp.Regexp, 0, len(specs))
	for _, s := range specs {
		if strings.TrimSpace(s.Text) == "" {
			return nil, focusErr("focus text is empty")
		}
		if utf8.RuneCountInString(s.Text) > MaxFocusTextLen {
			return nil, focusErr("focus text longer than %d characters", MaxFocusTextLen)
		}
		re, err := focusRegexp(s)
		if err != nil {
			return nil, focusErr("bad focus regex %q: %v", s.Text, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func focusRegexp(s FocusSpec) (*regexp.Regexp, error) {
	if s.Regex {
		return regexp.Compile("(?i)" + s.Text)
	}
	text := strings.TrimSpace(s.Text)
	alts := []string{literalPattern(text)}
	// Go and .NET serializers escape < > & as \u003c \u003e \u0026; the UI
	// inspector shows them decoded. Accept either spelling of the selection.
	for _, v := range []string{jsonEscapeHTML.Replace(text), jsonUnescapeHTML.Replace(text)} {
		if v != text {
			alts = append(alts, literalPattern(v))
		}
	}
	if len(alts) == 1 {
		return regexp.Compile("(?i)" + alts[0])
	}
	return regexp.Compile("(?i)(?:" + strings.Join(alts, "|") + ")")
}

var (
	jsonEscapeHTML   = strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "&", `\u0026`)
	jsonUnescapeHTML = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&", `\u003C`, "<", `\u003E`, ">")
)

// literalPattern quotes a literal but lets whitespace after ':' and ',' vary,
// so raw JSON finds the pretty-printed line and vice versa.
func literalPattern(text string) string {
	var b strings.Builder
	prev := rune(0)
	inSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}
		switch {
		case inSpace && (prev == ':' || prev == ','):
			b.WriteString(`\s*`)
		case inSpace:
			b.WriteString(`\s+`)
		case prev == ':' || prev == ',':
			b.WriteString(`\s*`)
		}
		inSpace = false
		b.WriteString(regexp.QuoteMeta(string(r)))
		prev = r
	}
	return b.String()
}

// ParseLineRanges parses "res:400-440,req:1-5" (or "12", "3-9" for both sides).
func ParseLineRanges(s string) ([]LineRange, error) {
	var out []LineRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		r, err := parseLineRange(part)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if len(out) > MaxLineRanges {
		return nil, focusErr("at most %d line ranges", MaxLineRanges)
	}
	return out, nil
}

func parseLineRange(part string) (LineRange, error) {
	r := LineRange{Side: SideBoth}
	if side, rest, ok := strings.Cut(part, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(side)) {
		case "req", "request":
			r.Side = SideReq
		case "res", "resp", "response":
			r.Side = SideRes
		case "both":
		default:
			return r, focusErr("line range %q: side must be req or res", part)
		}
		part = rest
	}
	from, to, isRange := strings.Cut(strings.TrimSpace(part), "-")
	a, errA := strconv.Atoi(strings.TrimSpace(from))
	b := a
	var errB error
	if isRange {
		b, errB = strconv.Atoi(strings.TrimSpace(to))
	}
	if errA != nil || errB != nil || a < 1 || b < a {
		return r, focusErr("line range %q must look like res:400-440", part)
	}
	r.From, r.To = a, b
	return r, nil
}

// String renders a range back in its query form ("res:400-440").
func (r LineRange) String() string {
	side := string(r.Side)
	if side == "" {
		side = string(SideBoth)
	}
	if r.From == r.To {
		return fmt.Sprintf("%s:%d", side, r.From)
	}
	return fmt.Sprintf("%s:%d-%d", side, r.From, r.To)
}

func appliesTo(spec, pane Side) bool { return spec == "" || spec == SideBoth || spec == pane }

// span is a byte range [start, end) inside one line or row.
type span struct{ start, end int }

// paneFocus is what one pane is asked to center on.
type paneFocus struct {
	hits   [][]span // highlighted spans per logical line
	ranges [][2]int // 0-based inclusive logical line ranges
}

func (f paneFocus) anchored() bool {
	if len(f.ranges) > 0 {
		return true
	}
	for _, h := range f.hits {
		if len(h) > 0 {
			return true
		}
	}
	return false
}

// findHits runs every applicable matcher over each logical line and returns
// merged spans per line plus how many occurrences each spec found.
func findHits(lines []string, res []*regexp.Regexp, specs []FocusSpec, side Side) ([][]span, []int) {
	hits := make([][]span, len(lines))
	counts := make([]int, len(specs))
	total := 0
	for i, re := range res {
		if !appliesTo(specs[i].Side, side) {
			continue
		}
		for ln, line := range lines {
			for _, m := range re.FindAllStringIndex(line, -1) {
				if m[1] <= m[0] || total >= maxFocusHits {
					continue
				}
				hits[ln] = append(hits[ln], span{m[0], m[1]})
				counts[i]++
				total++
			}
		}
	}
	for i := range hits {
		hits[i] = mergeSpans(hits[i])
	}
	return hits, counts
}

func mergeSpans(in []span) []span {
	if len(in) < 2 {
		return in
	}
	sort.Slice(in, func(a, b int) bool { return in[a].start < in[b].start })
	out := in[:1]
	for _, s := range in[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			last.end = max(last.end, s.end)
			continue
		}
		out = append(out, s)
	}
	return out
}

// paneRanges clamps the ranges that apply to side to its n lines. Ranges that
// start past the end are returned as missing.
func paneRanges(ranges []LineRange, side Side, n int) (kept [][2]int, missing []LineRange) {
	for _, r := range ranges {
		if !appliesTo(r.Side, side) {
			continue
		}
		if r.From > n {
			missing = append(missing, r)
			continue
		}
		kept = append(kept, [2]int{r.From - 1, min(r.To, n) - 1})
	}
	return kept, missing
}
