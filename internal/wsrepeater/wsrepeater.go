// Package wsrepeater opens a fresh WebSocket connection to a target, sends one
// message, and captures the frames the server returns — a "Repeater" for
// WebSockets. It speaks enough of RFC 6455 to do this with no external deps:
// the client handshake, masked client frames, and frame reading. TLS
// verification is skipped (wss), matching how Interseptor talks to targets.
package wsrepeater

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes (RFC 6455 §5.2).
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// maxReplyBytes caps the total payload retained across all collected reply
// messages. Each frame is already capped at 16 MB, but 64 replies could
// otherwise buffer ~1 GB; this bounds one repeater action to a sane amount for
// display. maxReplyMessages caps how many whole replies are kept and
// maxReplyFrames bounds the raw frames read, so a fragmented stream cannot loop
// forever.
const (
	// maxHandshakeBytes bounds the upgrade response (status line and headers).
	maxHandshakeBytes = 256 << 10
	maxReplyBytes     = 32 << 20
	maxReplyMessages  = 64
	maxReplyFrames    = 4096
)

// Request is one WebSocket send.
type Request struct {
	URL     string            // ws:// wss:// (http/https also accepted)
	Message string            // payload to send
	Binary  bool              // send a binary frame instead of text
	Headers map[string]string // extra handshake headers (Cookie, Authorization, …)
	ReadFor time.Duration     // how long to collect response frames (default 2s)
	// CheckDial, when set, vets every resolved address right before the
	// connection is made (net.Dialer.Control), so DNS names cannot reach a
	// destination the literal-host checks would have refused.
	CheckDial func(ip net.IP, port int) error
}

// dialControl adapts CheckDial to net.Dialer.Control.
func dialControl(check func(net.IP, int) error) func(network, address string, _ syscall.RawConn) error {
	if check == nil {
		return nil
	}
	return func(_, address string, _ syscall.RawConn) error {
		host, portStr, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("unresolved dial address %q", address)
		}
		return check(ip, port)
	}
}

// Frame is one sent or received WebSocket frame.
type Frame struct {
	Dir    string `json:"dir"` // "send" | "recv"
	Opcode int    `json:"opcode"`
	Text   string `json:"text"`
	Len    int    `json:"len"`
}

// Result is the handshake status plus the sent and received frames.
type Result struct {
	Status int     `json:"status"` // handshake HTTP status (101 on success)
	Frames []Frame `json:"frames"`

	// ReqHeaders / ResHeaders are the handshake as sent and as answered, kept
	// so the exchange can be recorded as a flow. Not part of the JSON reply.
	ReqHeaders map[string][]string `json:"-"`
	ResHeaders map[string][]string `json:"-"`
}

// Send performs the handshake, sends the message, and returns the exchange.
func Send(req Request) (*Result, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q", req.URL)
	}
	// A URL without a host would be dialled as ":80", which the OS takes to
	// mean this machine; a foreign scheme would silently be sent as plain ws.
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss", "http", "https":
	default:
		return nil, fmt.Errorf("invalid URL %q: scheme must be ws, wss, http or https", req.URL)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid URL %q: no host", req.URL)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	secure := u.Scheme == "wss" || u.Scheme == "https"
	host := u.Host
	if u.Port() == "" {
		if secure {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: dialControl(req.CheckDial)}
	var conn net.Conn
	if secure {
		conn, err = tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // pentest tool, by design
	} else {
		conn, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", host, err)
	}
	defer conn.Close()

	readFor := req.ReadFor
	if readFor <= 0 {
		readFor = 2 * time.Second
	}
	conn.SetDeadline(time.Now().Add(readFor + 10*time.Second))

	key := newKey()
	if err := writeHandshake(conn, u, key, req.Headers); err != nil {
		return nil, err
	}

	// The handshake is read through a byte budget: textproto buffers a header
	// line until its newline, so a server that never sends one would otherwise
	// grow the buffer until the deadline. Frames have their own caps, so the
	// budget is lifted once the handshake is in.
	lim := &io.LimitedReader{R: conn, N: maxHandshakeBytes}
	br := bufio.NewReader(lim)
	status, resHeaders, err := readHandshake(br)
	if err != nil {
		if lim.N <= 0 {
			return nil, fmt.Errorf("handshake response too large (over %d bytes)", maxHandshakeBytes)
		}
		return nil, err
	}
	lim.N = math.MaxInt64
	res := &Result{Status: status, ReqHeaders: headerMap(handshakeHeaders(u, key, req.Headers)), ResHeaders: resHeaders}
	accept := resHeaders.Get("Sec-WebSocket-Accept")
	if status != 101 {
		return res, fmt.Errorf("handshake failed: HTTP %d", status)
	}
	if accept != acceptKey(key) {
		return res, fmt.Errorf("bad Sec-WebSocket-Accept (handshake not honored)")
	}

	// Handshake done — reset the deadline so a slow handshake didn't consume the
	// frame-exchange budget (the send below and the read loop get a fresh window).
	conn.SetDeadline(time.Now().Add(readFor + 5*time.Second))

	// Send the message frame.
	opcode := byte(opText)
	if req.Binary {
		opcode = opBinary
	}
	if _, err := conn.Write(encodeClientFrame(opcode, []byte(req.Message))); err != nil {
		return res, fmt.Errorf("send frame: %w", err)
	}
	res.Frames = append(res.Frames, Frame{Dir: "send", Opcode: int(opcode), Text: req.Message, Len: len(req.Message)})

	// Collect reply messages until the read window closes or the server closes.
	// A message may be split over continuation frames (RFC 6455 §5.4), so
	// fragments are reassembled and one logical message is one reply; control
	// frames may arrive between fragments and are still handled on sight.
	conn.SetReadDeadline(time.Now().Add(readFor))
	total, messages, msgOp := 0, 0, -1
	var msg []byte
	for frames := 0; messages < maxReplyMessages && frames < maxReplyFrames; frames++ {
		fin, op, payload, err := readFrame(br)
		if err != nil {
			break
		}
		if op == opPing {
			// RFC 6455 §5.5.2: upon receipt of a Ping, send a Pong with matching payload
			_, _ = conn.Write(encodeClientFrame(opPong, payload))
			continue
		}
		if op == opPong {
			continue
		}
		if op == opClose {
			res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: op, Text: "(close)", Len: len(payload)})
			// RFC 6455 §5.5.1: answer a server-initiated close so the closing
			// handshake completes instead of looking like an abnormal closure
			// (1006) to the peer. Only the status code is echoed: a control
			// frame carries at most 125 bytes, and the received payload is not
			// bounded by that.
			_, _ = conn.Write(encodeClientFrame(opClose, closeReplyPayload(payload)))
			break
		}
		if op != opContinuation && msgOp >= 0 {
			// A new message started before the previous one finished: keep the
			// payload collected so far instead of dropping it silently.
			res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: msgOp, Text: string(msg), Len: len(msg)})
			total += len(msg)
			messages++
			msg, msgOp = nil, -1
		}
		if msgOp < 0 {
			msgOp = op
		}
		msg = append(msg, payload...)
		// The cap is on everything kept so far plus the message in progress.
		// Counting only finished messages let a peer that keeps abandoning
		// messages (a new data frame before FIN) pile up 64 of them.
		if total+len(msg) > maxReplyBytes {
			res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: msgOp, Text: string(msg), Len: len(msg)})
			msg, msgOp = nil, -1
			break
		}
		if !fin {
			continue
		}
		res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: msgOp, Text: string(msg), Len: len(msg)})
		total += len(msg)
		messages++
		msg, msgOp = nil, -1
		if total > maxReplyBytes {
			break
		}
	}
	// A message still in progress when the window closed, the peer went away or
	// a frame cap was reached is kept as received: dropping it showed "no
	// reply" for a server that had in fact answered.
	if msgOp >= 0 {
		res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: msgOp, Text: string(msg), Len: len(msg)})
	}
	return res, nil
}

type headerLine struct{ key, value string }

// handshakeHeaders lists the request headers of the client handshake. The
// mandatory upgrade headers always win over caller-supplied duplicates.
func handshakeHeaders(u *url.URL, key string, extra map[string]string) []headerLine {
	lines := []headerLine{
		{"Host", u.Host},
		{"Upgrade", "websocket"},
		{"Connection", "Upgrade"},
		{"Sec-WebSocket-Key", key},
		{"Sec-WebSocket-Version", "13"},
	}
	for k, v := range extra {
		if k == "" {
			continue
		}
		if strings.EqualFold(k, "Host") ||
			strings.EqualFold(k, "Upgrade") ||
			strings.EqualFold(k, "Connection") ||
			strings.EqualFold(k, "Sec-WebSocket-Key") ||
			strings.EqualFold(k, "Sec-WebSocket-Version") {
			continue
		}
		lines = append(lines, headerLine{k, v})
	}
	return lines
}

func headerMap(lines []headerLine) map[string][]string {
	m := make(map[string][]string, len(lines))
	for _, l := range lines {
		m[l.key] = append(m[l.key], l.value)
	}
	return m
}

func writeHandshake(conn net.Conn, u *url.URL, key string, extra map[string]string) error {
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", path)
	for _, l := range handshakeHeaders(u, key, extra) {
		fmt.Fprintf(&b, "%s: %s\r\n", l.key, l.value)
	}
	b.WriteString("\r\n")
	_, err := conn.Write([]byte(b.String()))
	return err
}

// readHandshake reads the upgrade response and returns its status code and
// headers. Subsequent frames remain buffered in br.
func readHandshake(br *bufio.Reader) (status int, hdr textproto.MIMEHeader, err error) {
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine() // "HTTP/1.1 101 Switching Protocols"
	if err != nil {
		return 0, nil, fmt.Errorf("read handshake: %w", err)
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return 0, nil, fmt.Errorf("malformed status line %q", line)
	}
	status, _ = strconv.Atoi(parts[1])
	hdr, err = tp.ReadMIMEHeader()
	if err != nil {
		return status, nil, fmt.Errorf("read handshake headers: %w", err)
	}
	return status, hdr, nil
}

func newKey() string {
	var b [16]byte
	rand.Read(b[:])
	return base64.StdEncoding.EncodeToString(b[:])
}

func acceptKey(key string) string {
	h := sha1.New() //nolint:gosec // mandated by RFC 6455
	h.Write([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// encodeClientFrame builds a masked, unfragmented client frame (clients MUST
// mask, RFC 6455 §5.3).
func encodeClientFrame(opcode byte, payload []byte) []byte {
	var mask [4]byte
	rand.Read(mask[:])

	var hdr []byte
	hdr = append(hdr, 0x80|opcode) // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 1<<16:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	hdr = append(hdr, mask[:]...)

	out := make([]byte, 0, len(hdr)+n)
	out = append(out, hdr...)
	for i, c := range payload {
		out = append(out, c^mask[i%4])
	}
	return out
}

// readFrame reads one frame (handles masked or unmasked) and reports whether it
// is the final frame of its message. The caller reassembles continuation
// frames; control frames may be interleaved with them.
func readFrame(br *bufio.Reader) (fin bool, opcode int, payload []byte, err error) {
	b0, err := br.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	b1, err := br.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	fin = b0&0x80 != 0
	opcode = int(b0 & 0x0f)
	masked := b1&0x80 != 0
	n := uint64(b1 & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err = readFull(br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = readFull(br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > 16<<20 {
		return false, 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	var mask [4]byte
	if masked {
		if _, err = readFull(br, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, n)
	if _, err = readFull(br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// closeReplyPayload returns the payload for the close frame that answers a
// peer-initiated close: its status code when one was sent, otherwise an empty
// close.
func closeReplyPayload(payload []byte) []byte {
	if len(payload) >= 2 {
		return payload[:2]
	}
	return nil
}

func readFull(br *bufio.Reader, p []byte) (int, error) {
	total := 0
	for total < len(p) {
		n, err := br.Read(p[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
