package control

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/store"
)

const maxFlowPreviewRequestBytes int64 = 64 << 10

// getFlowPreviewPNG renders an Interseptor-styled request/response PNG for a flow.
// Query: side=both|req|res, pretty=0|1, layout=vertical|horizontal, theme=dark|light.
func (h *flowAPI) getFlowPreviewPNG(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	opts := previewOptsFromQuery(r)
	pngBytes, err := h.renderFlowPreview(f, opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`inline; filename="flow-%d-preview.png"`, f.ID))
	_, _ = w.Write(pngBytes)
}

// attachFindingFlowPreview generates a flow PNG and attaches it as finding image evidence.
// Body: {flowId, side?, pretty?, layout?, theme?, caption?, position?}.
func (h *findingsAPI) attachFindingFlowPreview(w http.ResponseWriter, r *http.Request) {
	findingID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		FlowID   int64  `json:"flowId"`
		Side     string `json:"side"`
		Pretty   *bool  `json:"pretty"`
		Layout   string `json:"layout"`
		Theme    string `json:"theme"`
		Caption  string `json:"caption"`
		Position *int   `json:"position"`
	}
	if !decodeLimitedJSON(w, r, maxFlowPreviewRequestBytes, &in) {
		return
	}
	if in.FlowID <= 0 {
		httpErr(w, http.StatusBadRequest, "flowId is required")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil || f == nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	opts := preview.Options{
		Side:   preview.NormalizeSide(in.Side),
		Layout: preview.NormalizeLayout(in.Layout),
		Theme:  preview.NormalizeTheme(in.Theme),
		Title:  flowPreviewTitle(f),
	}
	pretty := true
	if in.Pretty != nil {
		pretty = *in.Pretty
	}
	opts.Pretty = preview.Bool(pretty)
	pngBytes, err := h.renderFlowPreview(f, opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	hash, _, err := h.st.PutImageBytes("image/png", pngBytes)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	caption := strings.TrimSpace(in.Caption)
	if caption == "" {
		caption = flowPreviewTitle(f)
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := h.st.AttachImage(findingID, hash, "image/png", caption, pos); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(findingID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func previewOptsFromQuery(r *http.Request) preview.Options {
	q := r.URL.Query()
	return preview.Options{
		Side:   preview.NormalizeSide(q.Get("side")),
		Layout: preview.NormalizeLayout(q.Get("layout")),
		Theme:  preview.NormalizeTheme(q.Get("theme")),
		Pretty: preview.Bool(preview.ParseBool(q.Get("pretty"), true)),
	}
}

func (h *Hub) renderFlowPreview(f *store.Flow, opts preview.Options) ([]byte, error) {
	var req, res []byte
	side := opts.Side
	if side == "" {
		side = preview.SideBoth
	}
	if side == preview.SideBoth || side == preview.SideReq {
		req = h.rawRequest(f)
	}
	if side == preview.SideBoth || side == preview.SideRes {
		res = h.rawResponse(f)
	}
	if opts.Title == "" {
		opts.Title = flowPreviewTitle(f)
	}
	return preview.Render(req, res, opts)
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
