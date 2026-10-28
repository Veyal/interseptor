package control

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
)

const maxFlowPreviewRequestBytes int64 = 64 << 10

// A flow image shows at most previewBodyCap bytes of each body. Bodies up to
// previewPrettyLimit are read whole so JSON is pretty-printed before the cut
// and line numbers match the inspector.
const (
	previewBodyCap     = 64 << 10
	previewPrettyLimit = 256 << 10
)

// getFlowPreviewPNG renders an Interseptor-styled request/response PNG for a flow.
// Query: side=both|req|res, pretty=0|1, layout=vertical|horizontal, theme=dark|light,
// maskHeaders=0|1, plus focus (repeatable), regex, focusSide, lines (repeatable),
// context, caption, mode=top|focus and decode=auto|raw. Focus outcome and any
// body decoding are reported in headers.
func (h *flowAPI) getFlowPreviewPNG(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	opts, err := previewOptsFromQuery(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Masking is for a preview copied into a ticket or chat; the attach-as-evidence
	// path below never masks, so a finding keeps its credentials verbatim.
	mask := preview.ParseBool(r.URL.Query().Get("maskHeaders"), false)
	decode, err := parseDecodeMode(r.URL.Query().Get("decode"))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	result, decoded, err := h.renderFlowPreview(f, opts, mask, decode)
	if err != nil {
		writePreviewError(w, err)
		return
	}
	writeFocusHeaders(w, opts.Side, result)
	writeDecodeHeader(w, decoded)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`inline; filename="flow-%d-preview.png"`, f.ID))
	_, _ = w.Write(result.PNG)
}

// flowPreviewAttachInput is the POST /api/findings/{id}/flow-preview body.
type flowPreviewAttachInput struct {
	FlowID    int64       `json:"flowId"`
	Side      string      `json:"side"`
	Pretty    *bool       `json:"pretty"`
	Layout    string      `json:"layout"`
	Theme     string      `json:"theme"`
	Caption   string      `json:"caption"`
	Position  *int        `json:"position"`
	Role      string      `json:"role"`
	Proof     string      `json:"proof"`
	Focus     flexStrings `json:"focus"`
	Regex     bool        `json:"regex"`
	FocusSide string      `json:"focusSide"`
	Lines     string      `json:"lines"`
	Context   *int        `json:"context"`
	Mode      string      `json:"mode"`
	Decode    string      `json:"decode"`
}

// attachOptions builds render options; a focused image also draws the caption.
func (in flowPreviewAttachInput) attachOptions() (preview.Options, bool, error) {
	opts := preview.Options{
		Side:   preview.NormalizeSide(in.Side),
		Layout: preview.NormalizeLayout(in.Layout),
		Theme:  preview.NormalizeTheme(in.Theme),
		Pretty: preview.Bool(in.Pretty == nil || *in.Pretty),
	}
	focus := previewFocusInput{Focus: in.Focus, Regex: in.Regex, FocusSide: in.FocusSide, Context: in.Context, Caption: in.Caption, Mode: in.Mode}
	if strings.TrimSpace(in.Lines) != "" {
		focus.Lines = []string{in.Lines}
	}
	focused, err := focus.apply(&opts)
	return opts, focused, err
}

// focusMissed reports whether a focused attach drew no focus at all: no line
// range was kept and no match could be drawn (none found, masked only, or cut
// by the row budget).
func focusMissed(opts preview.Options, r preview.RenderResult) bool {
	if r.Mode != preview.ModeFocus {
		return true
	}
	for _, l := range opts.Lines {
		if !slices.Contains(r.NotFound, l.String()) {
			return false
		}
	}
	for s, n := range r.Matches {
		if n > 0 && !slices.Contains(r.NotShown, s) {
			return false
		}
	}
	return true
}

// attachFindingFlowPreview generates a flow PNG and attaches it as finding image evidence.
// Body: {flowId, side?, pretty?, layout?, theme?, caption?, position?, role?, proof?,
// focus?, regex?, focusSide?, lines?, context?, mode?, decode?}.
func (h *findingsAPI) attachFindingFlowPreview(w http.ResponseWriter, r *http.Request) {
	findingID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !h.requireFinding(w, findingID) {
		return
	}
	var in flowPreviewAttachInput
	if !decodeLimitedJSON(w, r, maxFlowPreviewRequestBytes, &in) {
		return
	}
	if in.FlowID <= 0 {
		httpErr(w, http.StatusBadRequest, "flowId is required")
		return
	}
	opts, focused, err := in.attachOptions()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	if f == nil {
		httpInternalErr(w, fmt.Errorf("flow %d lookup returned nil without error", in.FlowID))
		return
	}
	decode, err := parseDecodeMode(in.Decode)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.Title = flowPreviewTitle(f)
	result, decoded, err := h.renderFlowPreview(f, opts, false, decode)
	if err != nil {
		writePreviewError(w, err)
		return
	}
	if focused && focusMissed(opts, result) {
		// Never store a "focused" image that shows no focus: the caption and
		// provenance would claim a highlight the image does not carry.
		writeFocusHeaders(w, opts.Side, result)
		writeDecodeHeader(w, decoded)
		writeJSON(w, http.StatusUnprocessableEntity, focusNotFoundBody(result))
		return
	}
	caption := strings.TrimSpace(in.Caption)
	if caption == "" {
		caption = flowPreviewTitle(f)
		if focused {
			caption = focusCaption(caption, opts)
		}
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	ref := ""
	if focused {
		ref = focusSourceRef(in.FlowID, opts)
	}
	_, _, err = h.st.PutAndAttachImageRef(findingID, "image/png", result.PNG, caption, pos, in.Role, in.Proof, "flow_preview", in.FlowID, ref, findingAPIChange(""))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(findingID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeFocusHeaders(w, opts.Side, result)
	writeDecodeHeader(w, decoded)
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

// focusNotFoundBody is the 422 body of a focused attach that drew nothing.
func focusNotFoundBody(r preview.RenderResult) map[string]any {
	items := append([]string{}, r.NotFound...)
	msg := "focus not found: " + strings.Join(items, ", ")
	if len(items) == 0 && r.MaskedMatch {
		msg = "focus not found: it matched only a masked value"
	} else if len(items) == 0 {
		msg = "focus not found: matches did not fit in the image; use lines or a nearer string"
	}
	out := map[string]any{"error": msg, "notFound": items}
	if len(r.BodyTruncated) > 0 {
		out["bodyTruncated"] = r.BodyTruncated
	}
	return out
}

func previewOptsFromQuery(r *http.Request) (preview.Options, error) {
	q := r.URL.Query()
	opts := preview.Options{
		Side:   preview.NormalizeSide(q.Get("side")),
		Layout: preview.NormalizeLayout(q.Get("layout")),
		Theme:  preview.NormalizeTheme(q.Get("theme")),
		Pretty: preview.Bool(preview.ParseBool(q.Get("pretty"), true)),
	}
	in, err := focusFromQuery(q)
	if err != nil {
		return opts, err
	}
	_, err = in.apply(&opts)
	return opts, err
}

func (h *Hub) renderFlowPreview(f *store.Flow, opts preview.Options, maskHeaders bool, decode string) (preview.RenderResult, previewDecodeInfo, error) {
	var info previewDecodeInfo
	dec := h.newPreviewDecoder(f, decode)
	var req, res []byte
	side := opts.Side
	if side == "" {
		side = preview.SideBoth
	}
	opts.BodyCap = previewBodyCap
	opts.BodySize = map[preview.Side]int64{}
	if side == preview.SideBoth || side == preview.SideReq {
		var err error
		var size int64
		req, size, info.Req, err = h.flowSideForPreview(f, true, dec)
		if err != nil {
			return preview.RenderResult{}, info, err
		}
		opts.BodySize[preview.SideReq] = size
	}
	if side == preview.SideBoth || side == preview.SideRes {
		var err error
		var size int64
		res, size, info.Res, err = h.flowSideForPreview(f, false, dec)
		if err != nil {
			return preview.RenderResult{}, info, err
		}
		opts.BodySize[preview.SideRes] = size
	}
	if opts.Title == "" {
		opts.Title = flowPreviewTitle(f)
	}
	if maskHeaders {
		// Mask credential header values only; the request/status line, other
		// headers and the body are rendered as captured. The renderer applies
		// it so a focus text found only in a masked value can be reported.
		opts.Mask = redact.CredentialHeaders
	}
	opts.Notices = info.Notices()
	opts.Deadline = time.Now().Add(renderTimeout)
	result, err := preview.RenderFlow(req, res, opts)
	return result, info, err
}

// flowRawSideForPreview builds one side's raw message for the renderer. The
// body is read up to previewPrettyLimit (whole when it fits, so the renderer
// can pretty-print before its 64 KiB cut); a larger body is passed as its
// first previewBodyCap bytes. size is the full body size when known (0 for an
// encoded body cut while decoding), so the image can say it was truncated.
func (h *Hub) flowRawSideForPreview(f *store.Flow, request bool) (raw []byte, size int64, err error) {
	raw, size, _, err = h.flowSideForPreview(f, request, nil)
	return raw, size, err
}

// flowSideForPreview is flowRawSideForPreview plus body decoding: a non-nil
// dec replaces the body with its readable form (codec, form, base64). The
// request line and headers stay as captured. A decoded body larger than
// previewPrettyLimit is cut like any other, with its full size reported.
func (h *Hub) flowSideForPreview(f *store.Flow, request bool, dec *previewDecoder) (raw []byte, size int64, dd sideDecode, err error) {
	var b strings.Builder
	var headers map[string][]string
	var hash, host string
	var stored int64
	if request {
		fmt.Fprintf(&b, "%s %s %s\r\n", f.Method, orVal(f.Path, "/"), orVal(f.HTTPVersion, "HTTP/1.1"))
		headers, hash, host, stored = f.ReqHeaders, f.ReqBodyHash, f.Host, f.ReqLen
	} else {
		fmt.Fprintf(&b, "%s %d %s\r\n", orVal(f.HTTPVersion, "HTTP/1.1"), f.Status, http.StatusText(f.Status))
		headers, hash, stored = f.ResHeaders, f.ResBodyHash, f.ResLen
	}
	displayHeaders, body, truncated, err := h.bodyForDisplayLimit(hash, headers, previewPrettyLimit)
	if err != nil {
		return nil, 0, dd, err
	}
	size = int64(len(body))
	if dec != nil && len(body) > 0 {
		var text string
		var ok bool
		if text, dd, ok = dec.decodeSide(request, displayHeaders, body, truncated); ok {
			// The decoded text is complete, so its size is known exactly.
			body, truncated, size = []byte(text), false, int64(len(text))
			if len(body) > previewPrettyLimit {
				body = body[:previewBodyCap]
				truncated = true
			}
		}
	}
	if truncated && dd.Tag == "" {
		size = 0
		if firstHeader(displayHeaders, "X-Interseptor-Decoded") == "" && stored > int64(len(body)) {
			size = stored
		}
		if size == 0 {
			size = int64(len(body)) + 1 // known to be larger; exact size unknown
		}
		body = body[:min(len(body), previewBodyCap)]
	}
	var out bytes.Buffer
	out.WriteString(b.String())
	writeHeaders(&out, displayHeaders, host)
	out.WriteString("\r\n")
	out.Write(body)
	return out.Bytes(), size, dd, nil
}

func writePreviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		httpErr(w, http.StatusNotFound, "flow body not found")
		return
	}
	if errors.Is(err, preview.ErrInvalidFocus) {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, preview.ErrRenderTimeout) {
		httpErr(w, http.StatusServiceUnavailable, "render timed out; retry")
		return
	}
	httpInternalErr(w, err)
}

func flowPreviewTitle(f *store.Flow) string {
	if f == nil {
		return ""
	}
	title := strings.TrimSpace(f.Method + " " + f.Host + f.Path)
	if f.Status > 0 {
		title += fmt.Sprintf(" → %d", f.Status)
	}
	return title
}
