package mcp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/preview"
)

const renderFlowPreviewDesc = "Render a captured flow as an Interseptor-styled HTTP request/response PNG. With findingId it is attached as source=flow_preview (sourceFlowId kept): generated report evidence, never a browser screenshot; add a proof naming the security-relevant line. Without findingId it returns a data URL. " +
	"By default only the first 80 lines of each side are drawn. When a body is long, the proof is not in its first 80 lines, or you want a reviewer to land on the exact evidence, pass focus with the exact proof text (a distinctive string such as \"isAdmin\":true, the reflected payload, or the error message) or lines (e.g. res:400-440). " +
	"The image then keeps the first line, collapses headers, shows context lines around each match with the match highlighted, and marks hidden lines with line numbers. Prefer an exact, distinctive string over a common word, and add a caption saying what is highlighted. " +
	"A soft-wrapped line too long for the image (minified JSON, HTML, base64) is clipped around the match with \"… N chars hidden …\" markers. Each body is drawn from its first 64 KiB (JSON up to 256 KiB is pretty-printed first, so line numbers match the inspector). " +
	"Read the result: it reports matches per side. If focus was not found the image falls back to the first lines with a visible notice, and an attach (findingId) is refused with nothing stored; copy the exact text from get_flow once and retry, or pass mode=top to attach the first lines. " +
	"If the result says the body was cut at 64 KiB, the text may lie beyond the image: use lines= or a string nearer the start instead of retrying the same text. " +
	"decode=auto (default) draws a codec, form-urlencoded or whole-base64 body as decoded text with a notice band; focus and lines= then match the decoded body. Use decode=raw to show the original bytes (wire format) as evidence."

// argFocusTexts accepts a single string or an array of strings. Commas stay
// inside a text: they are common in proof strings.
func argFocusTexts(a map[string]any) []string {
	switch v := a["focus"].(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// argDecode validates the decode argument: "" (unset), auto or raw.
func argDecode(a map[string]any) (string, error) {
	v := strings.ToLower(strings.TrimSpace(argStr(a, "decode")))
	if v != "" && v != "auto" && v != "raw" {
		return "", fmt.Errorf("decode must be auto or raw")
	}
	return v, nil
}

// decodeReport turns X-Interseptor-Decoded-Body into agent guidance.
func decodeReport(h http.Header) string {
	v := ""
	if h != nil {
		v = h.Get("X-Interseptor-Decoded-Body")
	}
	if v == "" {
		return ""
	}
	return "\nDecoded body: " + strings.ReplaceAll(v, ",", ", ") + ". The image shows the decoded text (focus and lines= refer to it); pass decode=raw to show the original bytes."
}

// flowPreviewFocusArgs copies the focus arguments into an attach body.
func flowPreviewFocusArgs(a map[string]any, body map[string]any) {
	if texts := argFocusTexts(a); len(texts) > 0 {
		body["focus"] = texts
	}
	if argBool(a, "regex", false) {
		body["regex"] = true
	}
	for _, k := range []string{"focusSide", "mode"} {
		if v := strings.TrimSpace(argStr(a, k)); v != "" {
			body[k] = v
		}
	}
	if v := argLines(a, "lines"); v != "" {
		body["lines"] = v
	}
	if _, ok := a["context"]; ok {
		body["context"] = argInt(a, "context", preview.DefaultFocusContext)
	}
}

// flowPreviewFocusQuery is the same set as preview.png query parameters.
func flowPreviewFocusQuery(a map[string]any, q url.Values) {
	body := map[string]any{}
	flowPreviewFocusArgs(a, body)
	if texts, ok := body["focus"].([]string); ok {
		q["focus"] = texts
	}
	if body["regex"] == true {
		q.Set("regex", "1")
	}
	for _, k := range []string{"focusSide", "lines", "mode"} {
		if v, ok := body[k].(string); ok {
			q.Set(k, v)
		}
	}
	if v, ok := body["context"].(int); ok {
		q.Set("context", strconv.Itoa(v))
	}
	if c := strings.TrimSpace(argStr(a, "caption")); c != "" {
		q.Set("caption", c)
	}
}

// focusReport turns the X-Interseptor-Focus-* headers into agent guidance.
func focusReport(h http.Header) string {
	if h == nil || h.Get("X-Interseptor-Focus-Matches") == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nFocus: mode=%s, matches %s.", h.Get("X-Interseptor-Focus-Mode"), strings.ReplaceAll(h.Get("X-Interseptor-Focus-Matches"), ",", " "))
	if nf := h.Get("X-Interseptor-Focus-Notfound"); nf != "" {
		var items []string
		for _, part := range strings.Split(nf, ",") {
			if v, err := url.PathUnescape(part); err == nil {
				part = v
			}
			items = append(items, strconv.Quote(part))
		}
		fmt.Fprintf(&b, "\nNot found: %s.", strings.Join(items, ", "))
		switch {
		case h.Get("X-Interseptor-Body-Truncated") != "":
			b.WriteString(" " + truncatedAdvice)
		case h.Get("X-Interseptor-Focus-Mode") == preview.ModeTop:
			b.WriteString(" The image shows the first lines with a not-found notice. Copy the exact proof text from get_flow (or pass lines) and render again.")
		}
	}
	if h.Get("X-Interseptor-Focus-Masked") == "1" {
		b.WriteString("\nA focus text occurs only in a masked credential header value; it stays masked and is not repeated here.")
	}
	if ns := h.Get("X-Interseptor-Focus-Notshown"); ns != "" {
		fmt.Fprintf(&b, "\nMatched, but none of the %s matches fit in the image: do not describe a highlight; pass lines= around the match or a nearer string.", ns)
	}
	if bt := h.Get("X-Interseptor-Body-Truncated"); bt != "" && h.Get("X-Interseptor-Focus-Notfound") == "" {
		fmt.Fprintf(&b, "\nBody truncated (%s): the image shows only the first 64 KiB of the body.", bt)
	}
	return b.String()
}

const truncatedAdvice = "the body was cut at 64 KiB for the image; the text may lie beyond it — use lines= or a nearer string (retrying the same text will not find it)."

// attachFocusError turns a refused focused attach (422) into guidance.
func attachFocusError(err error, flowID int) error {
	msg := err.Error()
	if !strings.Contains(msg, "→ 422") {
		return err
	}
	guide := fmt.Sprintf("Nothing was attached: the focus was not found in flow %d (%s). Copy the exact proof text from get_flow and retry once, pass lines= with the gutter line numbers, or pass mode=top to attach the first lines without focus.", flowID, msg)
	if strings.Contains(msg, "bodyTruncated") {
		guide += " Note: " + truncatedAdvice
	}
	return errors.New(guide)
}

func (s *Server) renderFlowPreview(a map[string]any) (string, error) {
	flowID, err := reqInt(a, "flowId")
	if err != nil {
		return "", err
	}
	if flowID == 0 {
		return "", fmt.Errorf("flowId is required (non-zero integer)")
	}
	side := strings.ToLower(strings.TrimSpace(argStr(a, "side")))
	if side == "" {
		side = "both"
	}
	decode, err := argDecode(a)
	if err != nil {
		return "", err
	}
	layout := strings.ToLower(strings.TrimSpace(argStr(a, "layout")))
	theme := strings.ToLower(strings.TrimSpace(argStr(a, "theme")))
	pretty := argBool(a, "pretty", true)
	if fid := argInt(a, "findingId", 0); fid > 0 {
		reqBody := map[string]any{
			"flowId": flowID, "side": side, "pretty": pretty, "layout": layout, "theme": theme,
			"caption": argStr(a, "caption"), "role": argStr(a, "role"), "proof": argStr(a, "proof"),
		}
		if pos, ok := a["position"]; ok && pos != nil {
			reqBody["position"] = pos
		}
		flowPreviewFocusArgs(a, reqBody)
		if decode != "" {
			reqBody["decode"] = decode
		}
		out, hdr, err := s.apiWithHeaders(http.MethodPost, fmt.Sprintf("/api/findings/%d/flow-preview", fid), reqBody)
		if err != nil {
			return "", attachFocusError(err, flowID)
		}
		return out + focusReport(hdr) + decodeReport(hdr), nil
	}
	q := url.Values{}
	q.Set("side", side)
	q.Set("pretty", map[bool]string{true: "1", false: "0"}[pretty])
	if layout != "" {
		q.Set("layout", layout)
	}
	if theme == "" {
		theme = string(preview.ThemeLight)
	}
	q.Set("theme", theme)
	flowPreviewFocusQuery(a, q)
	if decode != "" {
		q.Set("decode", decode)
	}
	raw, hdr, err := s.apiWithHeaders(http.MethodGet, fmt.Sprintf("/api/flows/%d/preview.png?%s", flowID, q.Encode()), nil)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(raw))
	return fmt.Sprintf("PNG preview for flow %d (%d bytes, side=%s pretty=%v layout=%s theme=%s).%s\nmime=image/png\nURL: /api/flows/%d/preview.png?%s\ndata:image/png;base64,%s\n\nTip: re-call with findingId to attach directly to a finding.",
		flowID, len(raw), side, pretty, preview.NormalizeLayout(layout), preview.NormalizeTheme(theme), focusReport(hdr)+decodeReport(hdr), flowID, q.Encode(), b64), nil
}
