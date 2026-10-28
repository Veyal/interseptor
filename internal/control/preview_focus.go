package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/preview"
)

// previewFocusInput is the focus part of a flow preview request, shared by
// GET /api/flows/{id}/preview.png (query) and POST /api/findings/{id}/flow-preview
// (JSON body), so both render the same image from the same parameters.
type previewFocusInput struct {
	Focus     []string
	Regex     bool
	FocusSide string
	Lines     []string
	Context   *int
	Caption   string
	Mode      string // "", "top" or "focus"
}

// focusFromQuery reads focus (repeatable), regex, focusSide, lines (repeatable),
// context, caption and mode from a preview.png query.
func focusFromQuery(q url.Values) (previewFocusInput, error) {
	in := previewFocusInput{
		Focus:     q["focus"],
		Regex:     preview.ParseBool(q.Get("regex"), false),
		FocusSide: q.Get("focusSide"),
		Lines:     q["lines"],
		Caption:   q.Get("caption"),
		Mode:      q.Get("mode"),
	}
	if v := strings.TrimSpace(q.Get("context")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return in, fmt.Errorf("%w: context must be an integer 0..%d", preview.ErrInvalidFocus, preview.MaxFocusContext)
		}
		in.Context = &n
	}
	return in, nil
}

// flexStrings accepts a JSON string or an array of strings.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one != "" {
			*f = flexStrings{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("focus must be a string or an array of strings")
	}
	*f = many
	return nil
}

// apply validates the input and writes it into opts. It returns whether
// focus mode was requested (focus texts or line ranges after mode).
func (in previewFocusInput) apply(opts *preview.Options) (bool, error) {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode != "" && mode != preview.ModeTop && mode != preview.ModeFocus {
		return false, fmt.Errorf("%w: mode must be top or focus", preview.ErrInvalidFocus)
	}
	side := normalizeFocusSide(in.FocusSide)
	for _, text := range in.Focus {
		if strings.TrimSpace(text) == "" {
			continue
		}
		opts.Focus = append(opts.Focus, preview.FocusSpec{Side: side, Text: text, Regex: in.Regex})
	}
	for _, spec := range in.Lines {
		ranges, err := preview.ParseLineRanges(spec)
		if err != nil {
			return false, err
		}
		opts.Lines = append(opts.Lines, ranges...)
	}
	if in.Context != nil && (*in.Context < 0 || *in.Context > preview.MaxFocusContext) {
		return false, fmt.Errorf("%w: context must be 0..%d", preview.ErrInvalidFocus, preview.MaxFocusContext)
	}
	opts.Context = in.Context
	opts.Caption = strings.TrimSpace(in.Caption)
	if mode == preview.ModeTop {
		opts.Focus, opts.Lines = nil, nil
	}
	focused := len(opts.Focus) > 0 || len(opts.Lines) > 0
	if mode == preview.ModeFocus && !focused {
		return false, fmt.Errorf("%w: mode=focus needs focus text or lines", preview.ErrInvalidFocus)
	}
	return focused, preview.ValidateFocus(*opts)
}

func normalizeFocusSide(s string) preview.Side {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "req", "request":
		return preview.SideReq
	case "res", "resp", "response":
		return preview.SideRes
	}
	return preview.SideBoth
}

// Focus outcome headers on preview.png and flow-preview attach responses.
const (
	hdrFocusMode     = "X-Interseptor-Focus-Mode"
	hdrFocusMatches  = "X-Interseptor-Focus-Matches"
	hdrFocusNotFound = "X-Interseptor-Focus-Notfound"
	hdrFocusMasked   = "X-Interseptor-Focus-Masked"
	hdrFocusNotShown = "X-Interseptor-Focus-Notshown"
	hdrBodyTruncated = "X-Interseptor-Body-Truncated"
)

// writeFocusHeaders reports matches per rendered side ("req=0,res=2"), focus
// texts or ranges that matched nothing (each percent-encoded, comma separated;
// a text found only in a masked value is never listed), whether one only
// occurs in a masked header value, sides whose matches could not be drawn and
// sides whose body was cut for the image.
func writeFocusHeaders(w http.ResponseWriter, side preview.Side, r preview.RenderResult) {
	w.Header().Set(hdrFocusMode, r.Mode)
	var parts []string
	for _, s := range []preview.Side{preview.SideReq, preview.SideRes} {
		if side == preview.SideBoth || side == s {
			parts = append(parts, fmt.Sprintf("%s=%d", s, r.Matches[s]))
		}
	}
	w.Header().Set(hdrFocusMatches, strings.Join(parts, ","))
	if len(r.NotFound) > 0 {
		enc := make([]string, len(r.NotFound))
		for i, s := range r.NotFound {
			enc[i] = strings.ReplaceAll(url.PathEscape(s), ",", "%2C")
		}
		w.Header().Set(hdrFocusNotFound, strings.Join(enc, ","))
	}
	if r.MaskedMatch {
		w.Header().Set(hdrFocusMasked, "1")
	}
	if v := sidesValue(r.NotShown); v != "" {
		w.Header().Set(hdrFocusNotShown, v)
	}
	if v := sidesValue(r.BodyTruncated); v != "" {
		w.Header().Set(hdrBodyTruncated, v)
	}
}

// sidesValue renders a side list as req, res or both ("" when empty).
func sidesValue(sides []preview.Side) string {
	req, res := false, false
	for _, s := range sides {
		req = req || s == preview.SideReq
		res = res || s == preview.SideRes
	}
	switch {
	case req && res:
		return string(preview.SideBoth)
	case req:
		return string(preview.SideReq)
	case res:
		return string(preview.SideRes)
	}
	return ""
}

// focusSourceRef names a focused flow preview in image provenance, e.g.
// "flow:12:focus:text-lines" (sourceRef allows only [a-z0-9:_-]).
func focusSourceRef(flowID int64, opts preview.Options) string {
	var kinds []string
	text, regex := false, false
	for _, f := range opts.Focus {
		if f.Regex {
			regex = true
		} else {
			text = true
		}
	}
	if text {
		kinds = append(kinds, "text")
	}
	if regex {
		kinds = append(kinds, "regex")
	}
	if len(opts.Lines) > 0 {
		kinds = append(kinds, "lines")
	}
	if len(kinds) == 0 {
		return ""
	}
	return fmt.Sprintf("flow:%d:focus:%s", flowID, strings.Join(kinds, "-"))
}

// focusCaption is the default evidence caption for a focused preview: the
// flow title plus what it centers on, so the stored block records the focus.
func focusCaption(title string, opts preview.Options) string {
	var parts []string
	for _, f := range opts.Focus {
		if f.Regex {
			parts = append(parts, "/"+f.Text+"/")
		} else {
			parts = append(parts, "“"+strings.TrimSpace(f.Text)+"”")
		}
	}
	for _, r := range opts.Lines {
		parts = append(parts, "lines "+r.String())
	}
	c := title + " · focus: " + strings.Join(parts, ", ")
	if r := []rune(c); len(r) > 240 {
		c = string(r[:239]) + "…"
	}
	return c
}
