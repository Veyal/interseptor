package proxy

import (
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// serveH2MITM drives the client↔proxy leg over HTTP/2 once ALPN negotiated "h2"
// on the forged leaf. It reuses the same gate/forward/record pipeline as the
// HTTP/1.1 loop, so intercept holds, match-&-replace, scope, and capture all
// apply identically — the only difference is the client-facing framing.
//
// ServeConn blocks until the connection is torn down (client close, idle
// timeout, or a fatal framing error), mirroring the h1 read loop in
// handleConnect. h2 has no HTTP/1.1-style Upgrade, so the WebSocket path never
// reaches here; connection reuse is the http2.Server's own concern.
func (s *Server) serveH2MITM(conn net.Conn, dialHost, logicalHost string, port int) {
	srv := &http2.Server{IdleTimeout: tunnelIdleTimeout}
	srv.ServeConn(conn, &http2.ServeConnOpts{Handler: s.h2Handler(dialHost, logicalHost, port)})
}

// h2Handler bridges a single decoded HTTP/2 request to the shared forwarding
// pipeline and writes the response back over the same h2 stream.
func (s *Server) h2Handler(dialHost, logicalHost string, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = "https"
		r.URL.Host = hostPort(logicalHost, port, "https")
		r = withOriginDialTarget(r, originDialTargetAddress(dialHost, port))

		// buildFlow records the client-leg proto (HTTP/2.0) before we normalize
		// the request below, so History shows the client actually spoke h2.
		flow := buildFlow(r, "https", logicalHost, port, time.Now())

		// http.Transport does its own upstream ALPN negotiation and rejects a
		// request whose ProtoMajor is 2, so present the forwarded request as
		// HTTP/1.1. The recorded flow keeps the real client proto.
		r.Proto, r.ProtoMajor, r.ProtoMinor = "HTTP/1.1", 1, 1
		r.RequestURI = ""

		resp, dropped, err := s.gateAndForward(flow, r)
		if dropped {
			flow.DurationMs = time.Since(flow.TS).Milliseconds()
			s.record(flow)
			http.Error(w, "request dropped by interseptor", http.StatusBadGateway)
			return
		}
		if err != nil {
			flow.Status = http.StatusBadGateway
			flow.Error = "upstream: " + err.Error()
			flow.DurationMs = time.Since(flow.TS).Milliseconds()
			s.record(flow)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		s.writeResponseHTTP(w, resp, flow)
	})
}

// h2ALPN is the client-leg ALPN preference offered on the forged MITM leaf:
// try HTTP/2, fall back to HTTP/1.1 when the client doesn't negotiate it.
var h2ALPN = []string{"h2", "http/1.1"}
