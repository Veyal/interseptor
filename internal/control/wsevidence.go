package control

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/wsrepeater"
)

// wsPreviewMax bounds the payload prefix kept per recorded frame, matching the
// proxy's frame capture.
const wsPreviewMax = 512

// recordWSExchange stores a ws_send exchange as a flow plus its frames so a
// WebSocket finding can cite it like any HTTP flow. It is best-effort:
// failures are logged and return 0, never affecting the send result.
func (h *Hub) recordWSExchange(rawURL string, res *wsrepeater.Result, sendErr error, started time.Time, extraFlags int64) int64 {
	if res == nil {
		return 0
	}
	flow, ok := wsFlowFor(rawURL, res, sendErr, started, extraFlags)
	if !ok {
		return 0
	}
	id, err := h.st.InsertFlow(flow)
	if err != nil {
		log.Printf("control: record ws exchange flow: %v", err)
		return 0
	}
	flow.ID = id
	for _, fr := range res.Frames {
		if err := h.st.SaveWSFrame(&store.WSFrame{
			FlowID: id, TS: time.Now(), Dir: fr.Dir, Opcode: fr.Opcode,
			Length: int64(fr.Len), Preview: wsPreview(fr.Text),
		}); err != nil {
			log.Printf("control: record ws frame: %v", err)
			break
		}
	}
	_ = h.st.FlushWSFrames() // make frames inspectable as soon as the reply returns
	h.FlowCaptured(flow)
	return id
}

func wsFlowFor(rawURL string, res *wsrepeater.Result, sendErr error, started time.Time, extraFlags int64) (*store.Flow, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	scheme := "http"
	if u.Scheme == "wss" || u.Scheme == "https" {
		scheme = "https"
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	f := &store.Flow{
		TS: started, Method: http.MethodGet, Scheme: scheme, Host: u.Hostname(),
		Port: atoiOr(u.Port(), defaultPortFor(scheme)), Path: path, HTTPVersion: "HTTP/1.1",
		Status: res.Status, ReqHeaders: res.ReqHeaders, ResHeaders: res.ResHeaders,
		DurationMs: time.Since(started).Milliseconds(),
		Flags:      store.FlagWebSocket | store.FlagRepeater | extraFlags,
	}
	if sendErr != nil {
		f.Error = sendErr.Error()
	}
	return f, true
}

func wsPreview(text string) string {
	if len(text) > wsPreviewMax {
		return text[:wsPreviewMax]
	}
	return text
}

// putWSFrameNote annotates one WebSocket frame of a flow ("" clears).
func (h *flowAPI) putWSFrameNote(w http.ResponseWriter, r *http.Request) {
	flowID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	frameID, err2 := strconv.ParseInt(r.PathValue("frameId"), 10, 64)
	if err1 != nil || err2 != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if !decodeLimitedJSON(w, r, maxFlowMetadataRequestBytes, &in) {
		return
	}
	if len(in.Note) > store.MaxWSFrameNoteBytes {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("note exceeds %d bytes", store.MaxWSFrameNoteBytes))
		return
	}
	if err := h.st.SetWSFrameNote(flowID, frameID, in.Note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpErr(w, http.StatusNotFound, "frame not found")
			return
		}
		httpInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// wsFramesForReport renders a WebSocket flow's frames as plain text appended
// to its exported response, so the evidence shows what was sent and received.
func (h *findingsAPI) wsFramesForReport(f *store.Flow) string {
	if f.Flags&store.FlagWebSocket == 0 {
		return ""
	}
	frames, err := h.st.QueryWSFrames(f.ID, 200)
	if err != nil || len(frames) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("\r\n\r\n[WebSocket frames]\r\n")
	for _, fr := range frames {
		arrow := "<-"
		if fr.Dir == "send" {
			arrow = "->"
		}
		fmt.Fprintf(&b, "%s %s opcode=%d len=%d: %s\r\n", arrow, fr.Dir, fr.Opcode, fr.Length, oneLine(fr.Preview))
		if fr.Note != "" {
			fmt.Fprintf(&b, "   note: %s\r\n", oneLine(fr.Note))
		}
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}
