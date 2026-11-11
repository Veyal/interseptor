package wsrepeater

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// r2Frame builds an unmasked server frame.
func r2Frame(fin bool, opcode byte, payload string) []byte {
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	return append([]byte{b0, byte(len(payload))}, payload...)
}

// A message whose final fragment never arrives inside the read window (or
// whose connection drops mid-message) was discarded without a trace: the
// operator saw "no reply" although the server had answered. What was received
// must be kept, like a message abandoned for a new one already is.
func TestR2UnfinishedMessageAtWindowEndIsKept(t *testing.T) {
	addr := wsFragmentedServer(t, func(conn net.Conn, _ *bufio.Reader) {
		conn.Write(r2Frame(true, opText, "whole"))
		conn.Write(r2Frame(false, opText, "par"))
		conn.Write(r2Frame(false, opContinuation, "tial"))
		// no FIN: the server stalls (or dies) mid-message
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "hi", ReadFor: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := recvFrames(res)
	if len(got) != 2 || got[0].Text != "whole" {
		t.Fatalf("recv frames = %+v, want the whole message and the unfinished one", got)
	}
	if got[1].Text != "partial" || got[1].Opcode != opText || got[1].Len != len("partial") {
		t.Errorf("unfinished message = %+v, want text %q", got[1], "partial")
	}
}

// Nothing is invented when no message was in progress.
func TestR2NoPhantomFrameWithoutAnUnfinishedMessage(t *testing.T) {
	addr := wsFragmentedServer(t, func(conn net.Conn, _ *bufio.Reader) {
		conn.Write(r2Frame(true, opText, "whole"))
	})
	res, err := Send(Request{URL: "ws://" + addr + "/", Message: "hi", ReadFor: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := recvFrames(res); len(got) != 1 || got[0].Text != "whole" {
		t.Fatalf("recv frames = %+v", got)
	}
}
