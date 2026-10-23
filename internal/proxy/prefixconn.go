package proxy

import (
	"bufio"
	"net"
)

// prefixedConn is a net.Conn whose reads first drain bytes that were already
// buffered (by net/http's server reader before Hijack) and then continue from
// the underlying connection. A client may pipeline its TLS ClientHello in the
// same packet as the CONNECT request; without this, those bytes would be lost
// and the handshake would stall.
type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// withBufferedPrefix returns conn unchanged when the hijacked reader holds no
// buffered bytes, otherwise a prefixedConn that replays them before reading
// from conn.
func withBufferedPrefix(conn net.Conn, rw *bufio.ReadWriter) net.Conn {
	if rw == nil || rw.Reader == nil || rw.Reader.Buffered() == 0 {
		return conn
	}
	br := rw.Reader
	b, _ := br.Peek(br.Buffered())
	return &prefixedConn{Conn: conn, prefix: append([]byte(nil), b...)}
}
