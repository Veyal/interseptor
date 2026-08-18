package control

import (
	"net/http"
	"path/filepath"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

const maxCodecSource = 512 << 10

// CodecsDir returns the project-scoped codecs directory (…/codecs).
func (h *Hub) CodecsDir() string {
	if h.ProjectDir == "" {
		return ""
	}
	return filepath.Join(h.ProjectDir, "codecs")
}

func (h *Hub) flowForCodec(f *store.Flow) msgcodec.Flow {
	_, reqBody := decodeForDisplay(f.ReqHeaders, h.bodyBytes(f.ReqBodyHash))
	_, resBody := decodeForDisplay(f.ResHeaders, h.bodyBytes(f.ResBodyHash))
	return msgcodec.Flow{
		Method: f.Method, Scheme: f.Scheme, Host: f.Host, Port: f.Port,
		Path: f.Path, Status: f.Status, Mime: f.Mime,
		ReqHeaders: f.ReqHeaders, ResHeaders: f.ResHeaders,
		ReqBody: string(reqBody), ResBody: string(resBody),
	}
}

func (h *Hub) loadCodecs() []*msgcodec.Codec {
	dir := h.CodecsDir()
	if dir == "" {
		return nil
	}
	codecs, _ := msgcodec.LoadDir(dir)
	return codecs
}

func (h *checksAPI) listCodecs(w http.ResponseWriter, r *http.Request) {
	dir := h.CodecsDir()
	codecs := []msgcodec.Source{}
	if dir != "" {
		if got := msgcodec.List(dir); got != nil {
			codecs = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"codecs": codecs,
		"dir":    dir,
	})
}

func (h *checksAPI) getCodec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir := h.CodecsDir()
	if dir == "" || !msgcodec.Exists(dir, id) {
		httpErr(w, http.StatusNotFound, "codec not found")
		return
	}
	src, err := msgcodec.Read(dir, id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "codec not found")
		return
	}
	c, cerr := msgcodec.Compile(id, src)
	out := map[string]any{"id": id, "source": src}
	if cerr != nil {
		out["error"] = cerr.Error()
	} else {
		out["meta"] = c.Meta
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *checksAPI) saveCodec(w http.ResponseWriter, r *http.Request) {
	dir := h.CodecsDir()
	if dir == "" {
		httpErr(w, http.StatusBadRequest, "project codecs directory not configured")
		return
	}
	var in struct {
		Source string `json:"source"`
	}
	if !decodeLimitedJSON(w, r, maxCodecSource, &in) {
		return
	}
	id := r.PathValue("id")
	if err := msgcodec.Save(dir, id, in.Source); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "codecs.update"})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "saved": true})
}

func (h *checksAPI) deleteCodec(w http.ResponseWriter, r *http.Request) {
	dir := h.CodecsDir()
	id := r.PathValue("id")
	if dir == "" {
		httpErr(w, http.StatusBadRequest, "project codecs directory not configured")
		return
	}
	if err := msgcodec.Delete(dir, id); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "codecs.update"})
	w.WriteHeader(http.StatusNoContent)
}

// testCodec compiles source (or a saved id) and runs match+decode against a flow.
func (h *checksAPI) testCodec(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source  string `json:"source"`
		ID      string `json:"id"`
		FlowID  int64  `json:"flowId"`
		Side    string `json:"side"`
		RawBody string `json:"rawBody"`
		Host    string `json:"host"`
		Method  string `json:"method"`
		Path    string `json:"path"`
	}
	if !decodeLimitedJSON(w, r, maxCodecSource, &in) {
		return
	}
	side := in.Side
	if side == "" {
		side = "req"
	}

	// No source/id → try every project codec (Intercept / ad-hoc body decode).
	if in.Source == "" && in.ID == "" {
		flow := msgcodec.Flow{Method: in.Method, Host: in.Host, Path: in.Path}
		if in.FlowID > 0 || (in.Host == "" && in.RawBody == "") {
			f, err := h.resolveFlow(in.FlowID)
			if err != nil {
				httpNotFoundOrInternal(w, err, "flow not found")
				return
			}
			if f == nil {
				writeJSON(w, http.StatusOK, map[string]any{"note": "no captured flow to test against yet"})
				return
			}
			flow = h.flowForCodec(f)
			in.FlowID = f.ID
		}
		if in.RawBody != "" {
			if side == "res" {
				flow.ResBody = in.RawBody
			} else {
				flow.ReqBody = in.RawBody
			}
		}
		if in.Host != "" {
			flow.Host = in.Host
		}
		res, matched := msgcodec.TryDecode(h.loadCodecs(), flow, side)
		if !matched {
			writeJSON(w, http.StatusOK, map[string]any{"matched": false, "flowId": in.FlowID, "side": side})
			return
		}
		out := map[string]any{
			"matched": true, "flowId": in.FlowID, "side": side,
			"codecId": res.CodecID, "title": res.Title,
			"plaintext": res.Plaintext, "fields": res.Fields, "note": res.Note,
		}
		if res.Error != "" {
			out["error"] = res.Error
		}
		for _, c := range h.loadCodecs() {
			if c != nil && c.Meta.ID == res.CodecID {
				out["applyOnSend"] = c.Meta.ApplyOnSend
				break
			}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	src := in.Source
	id := in.ID
	if src == "" {
		dir := h.CodecsDir()
		var err error
		src, err = msgcodec.Read(dir, id)
		if err != nil {
			httpErr(w, http.StatusNotFound, "codec not found")
			return
		}
	}
	if id == "" {
		id = "test"
	}
	c, err := msgcodec.Compile(id, src)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	var flow msgcodec.Flow
	var flowID int64
	f, err := h.resolveFlow(in.FlowID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	if f != nil {
		flow = h.flowForCodec(f)
		flowID = f.ID
	}
	if in.RawBody != "" {
		if side == "res" {
			flow.ResBody = in.RawBody
		} else {
			flow.ReqBody = in.RawBody
		}
	}
	if in.Host != "" {
		flow.Host = in.Host
	}
	if in.Method != "" {
		flow.Method = in.Method
	}
	if in.Path != "" {
		flow.Path = in.Path
	}
	ok, merr := c.Match(flow, side)
	if merr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": merr.Error(), "flowId": flowID})
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false, "flowId": flowID, "side": side})
		return
	}
	res, derr := c.Decode(flow, side)
	out := map[string]any{
		"matched": true, "flowId": flowID, "side": side,
		"codecId": res.CodecID, "title": res.Title,
		"plaintext": res.Plaintext, "fields": res.Fields, "note": res.Note,
		"applyOnSend": c.Meta.ApplyOnSend,
	}
	if derr != nil {
		out["error"] = derr.Error()
	}
	if res.Error != "" {
		out["error"] = res.Error
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) resolveFlow(id int64) (*store.Flow, error) {
	if id > 0 {
		return h.st.GetFlow(id)
	}
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(flows) == 0 {
		return nil, nil
	}
	return flows[0], nil
}

func (h *flowAPI) getFlowDecoded(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	side := r.URL.Query().Get("side")
	if side == "" {
		side = "req"
	}
	res, matched := msgcodec.TryDecode(h.loadCodecs(), h.flowForCodec(f), side)
	if !matched {
		writeJSON(w, http.StatusOK, map[string]any{
			"matched": false, "flowId": f.ID, "side": side,
		})
		return
	}
	out := map[string]any{
		"matched": true, "flowId": f.ID, "side": side,
		"codecId": res.CodecID, "title": res.Title,
		"plaintext": res.Plaintext, "fields": res.Fields, "note": res.Note,
	}
	if res.Error != "" {
		out["error"] = res.Error
	}
	// Surface apply_on_send from the matching codec meta when available.
	for _, c := range h.loadCodecs() {
		if c != nil && c.Meta.ID == res.CodecID {
			out["applyOnSend"] = c.Meta.ApplyOnSend
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// encodeCodecBody rebuilds a wire body from edited plaintext via a codec.
// Body: {source?, id?, flowId?, side, plaintext} — id/source identify the codec;
// flowId supplies match context (headers/host); omit for a synthetic empty flow.
func (h *checksAPI) encodeCodec(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source    string `json:"source"`
		ID        string `json:"id"`
		FlowID    int64  `json:"flowId"`
		Side      string `json:"side"`
		Plaintext string `json:"plaintext"`
		RawBody   string `json:"rawBody"` // optional wire body override for encode context
	}
	if !decodeLimitedJSON(w, r, maxCodecSource, &in) {
		return
	}
	src := in.Source
	id := in.ID
	if src == "" {
		if id == "" {
			httpErr(w, http.StatusBadRequest, "source or id required")
			return
		}
		var err error
		src, err = msgcodec.Read(h.CodecsDir(), id)
		if err != nil {
			httpErr(w, http.StatusNotFound, "codec not found")
			return
		}
	}
	if id == "" {
		id = "encode"
	}
	c, err := msgcodec.Compile(id, src)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	side := in.Side
	if side == "" {
		side = "req"
	}
	var flow msgcodec.Flow
	if in.FlowID > 0 {
		f, err := h.st.GetFlow(in.FlowID)
		if err != nil {
			httpNotFoundOrInternal(w, err, "flow not found")
			return
		}
		flow = h.flowForCodec(f)
	}
	if in.RawBody != "" {
		if side == "res" {
			flow.ResBody = in.RawBody
		} else {
			flow.ReqBody = in.RawBody
		}
	}
	wire, err := c.Encode(flow, side, in.Plaintext)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"codecId": c.Meta.ID, "side": side, "body": wire,
	})
}

// encode helper used by repeater when bodyMode=decoded.
func (h *Hub) encodeWithCodec(codecID string, flowID int64, side, plaintext, rawBody string) (string, error) {
	src, err := msgcodec.Read(h.CodecsDir(), codecID)
	if err != nil {
		return "", err
	}
	c, err := msgcodec.Compile(codecID, src)
	if err != nil {
		return "", err
	}
	var flow msgcodec.Flow
	if flowID > 0 {
		if f, err := h.st.GetFlow(flowID); err == nil {
			flow = h.flowForCodec(f)
		}
	}
	if rawBody != "" {
		if side == "res" {
			flow.ResBody = rawBody
		} else {
			flow.ReqBody = rawBody
		}
	}
	return c.Encode(flow, side, plaintext)
}
