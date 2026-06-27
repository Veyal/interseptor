// Package wsrepeater opens a fresh WebSocket connection to a target, sends one
// message, and captures the frames the server returns — a "Repeater" for
// WebSockets. It speaks enough of RFC 6455 to do this with no external deps:
// the client handshake, masked client frames, and frame reading. TLS
// verification is skipped (wss), matching how Interceptor talks to targets.
package wsrepeater

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes (RFC 6455 §5.2).
const (
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

// Request is one WebSocket send.
type Request struct {
	URL     string            // ws:// wss:// (http/https also accepted)
	Message string            // payload to send
	Binary  bool              // send a binary frame instead of text
	Headers map[string]string // extra handshake headers (Cookie, Authorization, …)
	ReadFor time.Duration     // how long to collect response frames (default 2s)
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
}

// Send performs the handshake, sends the message, and returns the exchange.
func Send(req Request) (*Result, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q", req.URL)
	}
	secure := u.Scheme == "wss" || u.Scheme == "https"
	host := u.Host
	if u.Port() == "" {
		if secure {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
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

	br := bufio.NewReader(conn)
	status, accept, err := readHandshake(br)
	if err != nil {
		return nil, err
	}
	res := &Result{Status: status}
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

	// Collect response frames until the read window closes or the server closes.
	conn.SetReadDeadline(time.Now().Add(readFor))
	for i := 0; i < 64; i++ {
		op, payload, err := readFrame(br)
		if err != nil {
			break
		}
		if op == opClose {
			res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: op, Text: "(close)", Len: len(payload)})
			break
		}
		if op == opPing || op == opPong {
			continue
		}
		res.Frames = append(res.Frames, Frame{Dir: "recv", Opcode: op, Text: string(payload), Len: len(payload)})
	}
	return res, nil
}

func writeHandshake(conn net.Conn, u *url.URL, key string, extra map[string]string) error {
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\n", key)
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	for k, v := range extra {
		if k == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	b.WriteString("\r\n")
	_, err := conn.Write([]byte(b.String()))
	return err
}

// readHandshake reads the upgrade response and returns its status code and the
// Sec-WebSocket-Accept header. Subsequent frames remain buffered in br.
func readHandshake(br *bufio.Reader) (status int, accept string, err error) {
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine() // "HTTP/1.1 101 Switching Protocols"
	if err != nil {
		return 0, "", fmt.Errorf("read handshake: %w", err)
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return 0, "", fmt.Errorf("malformed status line %q", line)
	}
	status, _ = strconv.Atoi(parts[1])
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return status, "", fmt.Errorf("read handshake headers: %w", err)
	}
	return status, hdr.Get("Sec-WebSocket-Accept"), nil
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

// readFrame reads one frame (handles masked or unmasked). Control-frame and
// fragmentation handling is intentionally minimal — enough for a repeater.
func readFrame(br *bufio.Reader) (opcode int, payload []byte, err error) {
	b0, err := br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	b1, err := br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	opcode = int(b0 & 0x0f)
	masked := b1&0x80 != 0
	n := uint64(b1 & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err = readFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = readFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > 16<<20 {
		return 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	var mask [4]byte
	if masked {
		if _, err = readFull(br, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, n)
	if _, err = readFull(br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
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
