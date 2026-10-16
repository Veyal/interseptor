package proxy

import (
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// classifyTLSError turns a failed MITM handshake into an operator-facing message.
// When the client sends CONNECT but rejects our leaf, pinning and an untrusted
// CA look the same on the wire — both abort the handshake.
func classifyTLSError(err error) string {
	if err == nil {
		return "tls: handshake failed"
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "eof"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "broken pipe"):
		return "tls: client closed during handshake — likely SSL pinning or untrusted CA (" + msg + ")"
	case strings.Contains(lower, "bad certificate"),
		strings.Contains(lower, "unknown certificate"),
		strings.Contains(lower, "certificate required"):
		return "tls: certificate rejected — " + msg
	default:
		return "tls handshake failed: " + msg
	}
}

// recordTLSFailure persists a CONNECT→TLS-failure event so operators can tell
// pinning/CA rejection apart from "no traffic at all".
func (s *Server) recordTLSFailure(host string, port int, clientAddr string, r *http.Request, started time.Time, handshakeErr error, suppression suppressionSnapshot) {
	flow := suppression.newFlow(&store.Flow{
		TS:          started,
		Method:      "CONNECT",
		Scheme:      "https",
		Host:        host,
		Port:        port,
		Path:        "(tls handshake)",
		HTTPVersion: "HTTP/1.1",
		ClientAddr:  clientAddr,
		Status:      0,
		Error:       classifyTLSError(handshakeErr),
		Flags:       store.FlagTLSFailed,
		DurationMs:  time.Since(started).Milliseconds(),
		ReqHeaders:  headerWithHost(r),
	})
	s.record(flow)
	if flow.ID != 0 {
		tags := []string{"tls-failed", "ssl-pinning?"}
		if s.pinningEnablerRecorded(host, started) {
			tags = append(tags, store.AnnotationPinningBlocked)
		}
		s.st.AddFlowTagsNonBlocking(flow.ID, tags)
	}
}

// pinningEnablerRecorded reports whether the project's interception setup
// lists a pinning-bypass enabler covering host. A handshake failure there is a
// capture gap (pinning blocked despite the enabler), so it is annotated rather
// than left to be read as a security finding. Lookup errors read as "no".
func (s *Server) pinningEnablerRecorded(host string, ts time.Time) bool {
	prov, err := s.st.InterceptionProvenance(ts, host)
	return err == nil && prov != nil && len(prov.Enablers) > 0
}
