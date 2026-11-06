package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// Capture is best-effort: when the response body cannot be recorded (unwritable
// bodies dir, ENOSPC, EMFILE) the client must still receive the body. The old
// plain-HTTP / h2 path wrote the response head, failed to tee, and then read
// nothing — so the client got the origin's Content-Length with zero bytes and
// hung or reported a truncation.
func TestCaptureFailureStillForwardsResponseBody(t *testing.T) {
	const payload = "hello-from-origin"

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, payload)
	}))
	defer origin.Close()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Make the content-addressed body directory unwritable so creating a body
	// writer fails, exactly like a full or read-only disk.
	bodies := filepath.Join(dir, "bodies")
	if err := os.MkdirAll(bodies, 0o700); err != nil {
		t.Fatalf("mkdir bodies: %v", err)
	}
	if err := os.Chmod(bodies, 0o500); err != nil {
		t.Fatalf("chmod bodies: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bodies, 0o700) })

	srv := New(st, capture.New(st), nil, nil, nil)
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("proxy URL: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	resp, err := client.Get(origin.URL + "/x")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v (body so far %q)", err, string(body))
	}
	if string(body) != payload {
		t.Fatalf("body = %q, want %q — a capture failure must not drop the response", string(body), payload)
	}
}
