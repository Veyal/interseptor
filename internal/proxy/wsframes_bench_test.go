package proxy

import (
	"bufio"
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// countConn is a net.Conn sink that counts Write calls (each one is a TLS
// record and a syscall on a real tunnel).
type countConn struct {
	net.Conn
	writes int
	buf    bytes.Buffer
}

func (c *countConn) Write(p []byte) (int, error) {
	c.writes++
	return c.buf.Write(p)
}
func (c *countConn) Close() error                     { return nil }
func (c *countConn) SetDeadline(time.Time) error      { return nil }
func (c *countConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countConn) SetWriteDeadline(time.Time) error { return nil }

func wsFrames(n int, payload string, masked bool) []byte {
	var b bytes.Buffer
	f := wsTextFrame(payload, masked)
	for i := 0; i < n; i++ {
		b.Write(f)
	}
	return b.Bytes()
}

// TestRelayWSFramesCoalescesSmallFrames checks that a small frame reaches the
// peer in one write (header + payload together) and byte-for-byte intact.
func TestRelayWSFramesCoalescesSmallFrames(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, nil, nil, nil, nil)
	in := wsFrames(10, "hello example.com", true)
	dst := &countConn{}
	_ = srv.relayWSFrames(1, "send", bufio.NewReader(bytes.NewReader(in)), dst)
	if !bytes.Equal(dst.buf.Bytes(), in) {
		t.Fatal("relayed frames differ from the input")
	}
	if dst.writes != 10 {
		t.Fatalf("10 small frames took %d writes, want 10 (one per frame)", dst.writes)
	}
	fr := waitWSFrames(t, s, 1, 10)
	if fr[0].Preview != "hello example.com" {
		t.Fatalf("preview = %q", fr[0].Preview)
	}
}

// wsFrame builds a frame of any length using the 7-bit, 16-bit or 64-bit
// length encoding as RFC 6455 requires.
func wsFrame(payload []byte, masked bool) []byte {
	f := []byte{0x82} // FIN + binary
	var mbit byte
	if masked {
		mbit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		f = append(f, mbit|byte(n))
	case n <= 0xffff:
		f = append(f, mbit|126, byte(n>>8), byte(n))
	default:
		f = append(f, mbit|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if !masked {
		return append(f, payload...)
	}
	mask := []byte{0xa1, 0xb2, 0xc3, 0xd4}
	f = append(f, mask...)
	for i, c := range payload {
		f = append(f, c^mask[i%4])
	}
	return f
}

// Every length encoding, masked or not, and frames larger than the copy
// buffer must be relayed byte-for-byte with an unmasked preview.
func TestRelayWSFramesAllLengthEncodings(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, nil, nil, nil, nil)
	sizes := []int{0, 1, 125, 126, 300, 65535, 32*1024 - 4, 32 * 1024, 70000}
	var in bytes.Buffer
	var payloads [][]byte
	for i, n := range sizes {
		p := bytes.Repeat([]byte{byte('a' + i)}, n)
		payloads = append(payloads, p)
		in.Write(wsFrame(p, i%2 == 0))
	}
	dst := &countConn{}
	_ = srv.relayWSFrames(2, "recv", bufio.NewReader(bytes.NewReader(in.Bytes())), dst)
	if !bytes.Equal(dst.buf.Bytes(), in.Bytes()) {
		t.Fatal("relayed bytes differ from the input")
	}
	frames := waitWSFrames(t, s, 2, len(sizes))
	byLen := map[int64]string{}
	for _, f := range frames {
		byLen[f.Length] = f.Preview
	}
	for i, n := range sizes {
		want := string(payloads[i][:min(n, wsPreviewMax)])
		if got, ok := byLen[int64(n)]; !ok || got != want {
			t.Fatalf("frame len %d preview = %q (found=%v), want %d unmasked bytes", n, got, ok, len(want))
		}
	}
}

func BenchmarkRelayWSFramesSmall(b *testing.B) {
	s, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, nil, nil, nil, nil)
	const perIter = 100
	in := wsFrames(perIter, `{"type":"ping","seq":1}`, true)
	b.ReportAllocs()
	b.SetBytes(int64(len(in)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := &countConn{}
		_ = srv.relayWSFrames(1, "send", bufio.NewReader(bytes.NewReader(in)), dst)
	}
}
