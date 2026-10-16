package control

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// wsTestServer completes one WebSocket handshake, reads one client frame and
// replies with an error text frame followed by a close frame.
func wsTestServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		tp := textproto.NewReader(br)
		_, _ = tp.ReadLine()
		hdr, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(hdr.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		// Client frame: 2-byte header, 4-byte mask, payload (short messages only).
		var head [2]byte
		if _, err := br.Read(head[:]); err != nil {
			return
		}
		n := int(head[1] & 0x7f)
		skip := make([]byte, 4+n)
		for read := 0; read < len(skip); {
			m, err := br.Read(skip[read:])
			if err != nil {
				return
			}
			read += m
		}
		msg := `{"type":"error","message":"Invalid token"}`
		conn.Write(append([]byte{0x81, byte(len(msg))}, msg...))
		conn.Write([]byte{0x88, 0x00})
	}()
	return ln.Addr().String()
}

func TestWSSendRecordsFlowAndFramesAsEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	addr := wsTestServer(t)

	body := fmt.Sprintf(`{"url":"ws://%s/stream?token=bad","message":"subscribe","headers":"X-Probe: 1"}`, addr)
	code, resp := doJSON(t, http.MethodPost, ts.URL+"/api/ws/send", body)
	if code != http.StatusOK {
		t.Fatalf("ws send = %d %s", code, resp)
	}
	var out struct {
		Status int   `json:"status"`
		FlowID int64 `json:"flowId"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil || out.FlowID == 0 || out.Status != 101 {
		t.Fatalf("response = %s (%v), want status 101 and a flowId", resp, err)
	}

	f, err := st.GetFlow(out.FlowID)
	if err != nil {
		t.Fatalf("flow %d not recorded: %v", out.FlowID, err)
	}
	if f.Status != 101 || f.Flags&store.FlagWebSocket == 0 || f.Flags&store.FlagRepeater == 0 {
		t.Fatalf("flow = status %d flags %b, want 101 + websocket + repeater", f.Status, f.Flags)
	}
	if !strings.HasPrefix(f.Path, "/stream?token=bad") || f.ReqHeaders["X-Probe"][0] != "1" {
		t.Fatalf("flow path/headers = %q %v", f.Path, f.ReqHeaders)
	}

	_ = st.FlushWSFrames()
	frames, err := st.QueryWSFrames(out.FlowID, 100)
	if err != nil || len(frames) < 3 {
		t.Fatalf("frames = %d err=%v, want send + error + close", len(frames), err)
	}
	if frames[0].Dir != "send" || frames[0].Preview != "subscribe" {
		t.Fatalf("first frame = %+v", frames[0])
	}
	if frames[1].Dir != "recv" || !strings.Contains(frames[1].Preview, "Invalid token") {
		t.Fatalf("second frame = %+v", frames[1])
	}

	// The flow is valid evidence: a finding can cite it and nothing is missing.
	fid, err := st.CreateFinding(&store.Finding{Title: "JWT in WebSocket URL", Severity: "Medium"})
	if err != nil {
		t.Fatal(err)
	}
	code, resp = doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/findings/%d/flows", ts.URL, fid), fmt.Sprintf(`{"flowId":%d,"note":"invalid token is rejected"}`, out.FlowID))
	if code >= 300 {
		t.Fatalf("attach ws flow to finding = %d %s", code, resp)
	}
	_, md := doJSON(t, http.MethodGet, ts.URL+"/api/findings/report?statuses=all", "")
	if !strings.Contains(md, "WebSocket frames") || !strings.Contains(md, "Invalid token") {
		t.Fatalf("report does not show WebSocket frames:\n%s", md)
	}
}

func TestWSFrameNoteRoute(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	flowID, _ := st.InsertFlow(&store.Flow{Method: "GET", Host: "example.com", Path: "/ws", Status: 101, Flags: store.FlagWebSocket})
	_ = st.SaveWSFrame(&store.WSFrame{FlowID: flowID, Dir: "recv", Opcode: 1, Length: 2, Preview: "ok"})
	_ = st.FlushWSFrames()
	frames, _ := st.QueryWSFrames(flowID, 10)

	url := fmt.Sprintf("%s/api/flows/%d/ws/%d/note", ts.URL, flowID, frames[0].ID)
	if code, _ := doJSON(t, http.MethodPut, url, `{"note":"auth accepted"}`); code != http.StatusNoContent {
		t.Fatalf("note status = %d, want 204", code)
	}
	_, body := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/flows/%d/ws", ts.URL, flowID), "")
	if !strings.Contains(body, "auth accepted") {
		t.Fatalf("frames listing lacks note: %s", body)
	}
	if code, _ := doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/flows/%d/ws/%d/note", ts.URL, flowID+1, frames[0].ID), `{"note":"x"}`); code != http.StatusNotFound {
		t.Fatalf("cross-flow note status = %d, want 404", code)
	}
}
