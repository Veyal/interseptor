package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A client that hangs up while a MITM (raw-conn) response body is still being
// written leaves only a fragment of the body in the store. writeResponseHTTP
// flags that as a capture error so History/replay do not present the fragment as
// the whole response; the raw-conn writer must record the same thing, or a
// truncated 200 reads as a complete one (same status, no error, short length).
func TestMITMClientAbortMidResponseMarksCaptureError(t *testing.T) {
	const size = 8 << 20
	h := newMITMAbortHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(size))
		chunk := strings.Repeat("A", 32*1024)
		for written := 0; written < size; written += len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	c := h.dial()
	fmt.Fprintf(c, "GET /big HTTP/1.1\r\nHost: %s\r\n\r\n", strings.TrimPrefix(h.origin.URL, "https://"))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 1024)); err != nil {
		t.Fatalf("read first body bytes: %v", err)
	}
	c.Close() // abort mid-body

	f := waitFlows(t, h.st, 1)[0]
	if f.ResLen >= size {
		t.Skipf("capture was not truncated (resLen=%d); nothing to assert", f.ResLen)
	}
	if f.Flags&store.FlagCaptureError == 0 {
		t.Fatalf("truncated response capture not flagged: resLen=%d of %d, flags=%v err=%q",
			f.ResLen, size, f.Flags, f.Error)
	}
}
