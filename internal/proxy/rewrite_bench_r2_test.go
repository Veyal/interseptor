package proxy

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// discardConn is a net.Conn that accepts and drops every write.
type discardConn struct{ net.Conn }

func (discardConn) Write(p []byte) (int, error) { return len(p), nil }
func (discardConn) Close() error                { return nil }

// rewriteBody is n bytes of text with a rule target roughly every KiB.
func rewriteBody(n int) []byte {
	unit := append(bytes.Repeat([]byte("x"), 1015), []byte("topsecret")...)
	return bytes.Repeat(unit, n/len(unit)+1)[:n]
}

// BenchmarkRewrittenResponse32MiB measures what one 32 MiB response costs on
// the MITM write path when a res-body rule rewrites it: bytes allocated beyond
// the body itself are copies the transform budget never charged.
func BenchmarkRewrittenResponse32MiB(b *testing.B) {
	st, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", BigBody: true, Match: "topsecret", Replace: "REDACTED!"}}); err != nil {
		b.Fatal(err)
	}
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.budget = newByteBudget(1 << 30)
	body := rewriteBody(32 << 20)
	req := httptest.NewRequest(http.MethodGet, "https://example.com/big", nil)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := &http.Response{
			StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Proto: "HTTP/1.1",
			Header:        http.Header{"Content-Type": {"text/plain"}},
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(bytes.NewReader(body)),
			Request:       req,
		}
		flow := srv.newFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/big"})
		if err := srv.writeResponseConn(discardConn{}, resp, flow); err != nil {
			b.Fatal(err)
		}
		if flow.Flags&store.FlagCaptureError != 0 || flow.OriginalResBodyHash == "" {
			b.Fatalf("response was not rewritten: flags=%v err=%q", flow.Flags, flow.Error)
		}
	}
}
