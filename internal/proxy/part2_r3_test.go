package proxy

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// The proxy credential is for the proxy. With proxy authentication off (or
// the client exempt) a client may still send Proxy-Authorization; it must not
// reach the origin and must not be stored in History.
func TestR3ProxyCredentialNeitherForwardedNorRecorded(t *testing.T) {
	seen := make(chan http.Header, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
	}))
	defer origin.Close()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(st, capture.New(st), nil, nil, nil)
	auth := &BasicAuth{} // disabled
	front := httptest.NewServer(auth.Handler(srv))
	defer front.Close()

	secret := "Basic " + base64.StdEncoding.EncodeToString([]byte("operator:hunter2"))
	req, _ := http.NewRequest("GET", origin.URL+"/x", nil)
	req.Header.Set("Proxy-Authorization", secret)
	tr := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(front.URL) }}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := (<-seen).Get("Proxy-Authorization"); got != "" {
		t.Fatalf("origin received the proxy credential %q", got)
	}
	f := waitFlows(t, st, 1)[0]
	for k, vs := range f.ReqHeaders {
		if strings.EqualFold(k, "Proxy-Authorization") || strings.Contains(strings.Join(vs, " "), "b3BlcmF0b3I6aHVudGVyMg") {
			t.Fatalf("History stores the proxy credential: %s: %q", k, vs)
		}
	}
}

// A frame whose 64-bit length has the top bit set (invalid per RFC 6455, and
// far more than any stream delivers) is relayed as far as the bytes go and
// must not be recorded with a negative length.
func TestR3WSFrameWithHugeLengthIsNotRecordedNegative(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(st, nil, nil, nil, nil)
	in := append(wsTextFrame("ok", false), 0x82, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 'a', 'b')
	dst := &countConn{}
	err = srv.relayWSFrames(7, "recv", bufio.NewReader(bytes.NewReader(in)), dst)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("relay error = %v, want an unexpected EOF inside the huge frame", err)
	}
	if !bytes.Equal(dst.buf.Bytes(), in) {
		t.Fatal("bytes were not relayed verbatim")
	}
	for _, fr := range waitWSFrames(t, st, 7, 1) {
		if fr.Length < 0 {
			t.Fatalf("frame recorded with negative length %d", fr.Length)
		}
	}
}
