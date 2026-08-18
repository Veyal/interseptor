package control

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/Veyal/interseptor/internal/mcp"
)

const (
	maxActivitySocketMessage  = 64 << 10
	activitySocketReadTimeout = 500 * time.Millisecond
)

type activitySocketMessage struct {
	Token    string       `json:"token"`
	Activity mcp.Activity `json:"activity"`
}

func (h *Hub) startActivityListener(listener net.Listener, token string) {
	h.activitySocket = listener
	h.activitySocketToken = token
	h.activitySocketWG.Add(1)
	go h.serveActivitySocket(listener)
}

func (h *Hub) serveActivitySocket(listener net.Listener) {
	defer h.activitySocketWG.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		h.activitySocketWG.Add(1)
		go h.readActivitySocket(conn)
	}
}

func (h *Hub) readActivitySocket(conn net.Conn) {
	defer h.activitySocketWG.Done()
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(activitySocketReadTimeout))
	decoder := json.NewDecoder(bufio.NewReader(io.LimitReader(conn, maxActivitySocketMessage)))
	activity := mcp.Activity{}
	if h.activitySocketToken == "" {
		if decoder.Decode(&activity) != nil {
			return
		}
	} else {
		var message activitySocketMessage
		if decoder.Decode(&message) != nil || subtle.ConstantTimeCompare([]byte(message.Token), []byte(h.activitySocketToken)) != 1 {
			return
		}
		activity = message.Activity
	}
	if activity.Tool != "" {
		if err := h.recordMCPActivityChecked(activity); err != nil {
			return
		}
		_, _ = conn.Write([]byte{1})
	}
}

func (h *Hub) closeActivitySocket() {
	if h.activitySocket == nil {
		return
	}
	_ = h.activitySocket.Close()
	h.activitySocketWG.Wait()
	h.removeActivitySocket()
}
