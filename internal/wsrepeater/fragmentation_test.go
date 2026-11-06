package wsrepeater

import (
	"bufio"
	"fmt"
	"net"
	"net/textproto"
	"testing"
	"time"
)

// wsFragmentedServer accepts one connection, completes the handshake, consumes
// the client frame, then writes the reply frames returned by write.
func wsFragmentedServer(t *testing.T, write func(conn net.Conn, br *bufio.Reader)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		if _, err := tp.ReadLine(); err != nil {
			return
		}
		hdr, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey(hdr.Get("Sec-WebSocket-Key"))); err != nil {
			return
		}
		_, _, _, _ = readFrame(br) // consume the client message
		write(conn, br)
		time.Sleep(150 * time.Millisecond)
	}()
	return ln.Addr().String()
}

func recvFrames(res *Result) []Frame {
	var out []Frame
	for _, f := range res.Frames {
		if f.Dir == "recv" {
			out = append(out, f)
		}
	}
	return out
}

// A message split over continuation frames (RFC 6455 §5.4) is one reply, not
// three partial ones: a server that fragments a payload (proxies commonly
// fragment at a few KB) must not look like it answered in pieces.
func TestSendReassemblesFragmentedMessage(t *testing.T) {
	addr := wsFragmentedServer(t, func(conn net.Conn, _ *bufio.Reader) {
		// FIN=0 text "frag", FIN=0 continuation "ment", FIN=1 continuation "ed".
		_, _ = conn.Write([]byte{0x01, 0x04, 'f', 'r', 'a', 'g'})
		_, _ = conn.Write([]byte{0x00, 0x04, 'm', 'e', 'n', 't'})
		_, _ = conn.Write([]byte{0x80, 0x02, 'e', 'd'})
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "hi", ReadFor: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	recv := recvFrames(res)
	if len(recv) != 1 || recv[0].Opcode != opText || recv[0].Text != "fragmented" || recv[0].Len != 10 {
		t.Fatalf("fragmented message not reassembled: %+v", recv)
	}
}

// A control frame interleaved between fragments is answered immediately and
// never splits the message being reassembled.
func TestSendReassemblesFragmentsAroundPing(t *testing.T) {
	pong := make(chan string, 1)
	addr := wsFragmentedServer(t, func(conn net.Conn, br *bufio.Reader) {
		_, _ = conn.Write([]byte{0x01, 0x03, 'a', 'b', 'c'})         // FIN=0 "abc"
		_, _ = conn.Write(encodeServerFrame(opPing, []byte("beat"))) // interleaved ping
		_, _, payload, err := readFrame(br)                          // client's pong
		if err == nil {
			pong <- string(payload)
		}
		_, _ = conn.Write([]byte{0x80, 0x03, 'd', 'e', 'f'}) // FIN=1 "def"
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "hi", ReadFor: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-pong:
		if got != "beat" {
			t.Fatalf("pong payload = %q, want heartbeat payload", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ping interleaved between fragments was never answered")
	}
	recv := recvFrames(res)
	if len(recv) != 1 || recv[0].Text != "abcdef" {
		t.Fatalf("fragments around a ping were not reassembled: %+v", recv)
	}
}

// A reply that is genuinely split over many fragments (more than the old
// 64-frame budget) still arrives whole.
func TestSendReassemblesManyFragments(t *testing.T) {
	const fragments = 100
	addr := wsFragmentedServer(t, func(conn net.Conn, _ *bufio.Reader) {
		for i := 0; i < fragments; i++ {
			fin := byte(0x00)
			if i == fragments-1 {
				fin = 0x80
			}
			if i == 0 {
				_, _ = conn.Write(append([]byte{fin | opText, 0x01}, 'x'))
				continue
			}
			_, _ = conn.Write(append([]byte{fin | opContinuation, 0x01}, 'x'))
		}
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "hi", ReadFor: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	recv := recvFrames(res)
	if len(recv) != 1 || len(recv[0].Text) != fragments {
		t.Fatalf("multi-fragment reply = %+v (want one message of %d bytes)", recv, fragments)
	}
}
