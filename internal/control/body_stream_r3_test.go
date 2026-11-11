package control

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// countingWriter is a ResponseWriter that keeps nothing, so a test measures
// what the handler allocates, not what the recorder buffers.
type countingWriter struct {
	hdr  http.Header
	n    int64
	code int
	head []byte
}

func (c *countingWriter) Header() http.Header {
	if c.hdr == nil {
		c.hdr = http.Header{}
	}
	return c.hdr
}
func (c *countingWriter) WriteHeader(code int) { c.code = code }
func (c *countingWriter) Write(p []byte) (int, error) {
	if len(c.head) < 256 {
		c.head = append(c.head, p[:min(len(p), 256-len(c.head))]...)
	}
	c.n += int64(len(p))
	return len(p), nil
}

func bigFlowR3(t *testing.T, st *store.Store, size int) (*store.Flow, []byte) {
	t.Helper()
	body := bytes.Repeat([]byte("0123456789abcde\n"), size/16)
	hash, n := putBodyR2(t, st, body)
	f := &store.Flow{TS: time.Now(), Method: "POST", Scheme: "https", Host: "app.example.com", Port: 443, Path: "/upload",
		Status: 200, Mime: "text/plain", ReqHeaders: map[string][]string{"Content-Type": {"text/plain"}},
		ResHeaders:  map[string][]string{"Content-Type": {"text/plain"}},
		ReqBodyHash: hash, ReqLen: n, ResBodyHash: hash, ResLen: n}
	if _, err := st.InsertFlow(f); err != nil {
		t.Fatal(err)
	}
	return f, body
}

// GET /api/flows/{id}/raw read the whole stored body into memory, then copied
// it into a second buffer behind the head: two body-sized allocations per
// request for a capture that can be hundreds of MB.
func TestFlowRawStreamsAPlainBody(t *testing.T) {
	h, st, _ := newHub(t)
	const size = 64 << 20
	f, body := bigFlowR3(t, st, size)
	for _, side := range []string{"req", "res"} {
		var w *countingWriter
		alloc := allocatedBy(func() {
			w = &countingWriter{}
			req := httptest.NewRequest(http.MethodGet, "/api/flows/1/raw?side="+side, nil)
			req.SetPathValue("id", strconv.FormatInt(f.ID, 10))
			(&flowAPI{h}).getFlowRaw(w, req)
		})
		t.Logf("raw side=%s: %d body bytes written, %.1f MiB allocated", side, w.n, float64(alloc)/(1<<20))
		if w.n < int64(len(body)) {
			t.Fatalf("side=%s wrote %d bytes, want the head plus %d body bytes", side, w.n, len(body))
		}
		if alloc > 4<<20 {
			t.Errorf("side=%s allocated %d MiB for a %d MiB body", side, alloc>>20, size>>20)
		}
	}
	// The bytes are unchanged: head, blank line, body.
	rec := httptest.NewRecorder()
	small, sb := bigFlowR3(t, st, 64)
	req := httptest.NewRequest(http.MethodGet, "/api/flows/2/raw?side=res", nil)
	req.SetPathValue("id", strconv.FormatInt(small.ID, 10))
	(&flowAPI{h}).getFlowRaw(rec, req)
	want := "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n" + string(sb)
	if rec.Body.String() != want {
		t.Fatalf("raw response changed:\n got %q\nwant %q", rec.Body.String(), want)
	}
}

func sliceAt(t *testing.T, h *Hub, id int64, offset, limit int) (flowSliceJSON, time.Duration) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/flows/x/slice?side=res&offset=%d&limit=%d", offset, limit), nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	start := time.Now()
	(&flowAPI{h}).getFlowSlice(rec, req)
	d := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("slice at %d: %d %s", offset, rec.Code, rec.Body.String())
	}
	var out flowSliceJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, d
}

// Reading page k of a plain body re-read and discarded everything before it,
// so scrolling a large body in the inspector was quadratic. Bodies are plain
// files: the window is reached by seeking. R3_SLICE_MB=200 measures the 200 MB
// case from the brief.
func TestFlowSlicePagingDoesNotRereadFromTheStart(t *testing.T) {
	h, st, _ := newHub(t)
	mb := 48
	if v, err := strconv.Atoi(os.Getenv("R3_SLICE_MB")); err == nil && v > 0 {
		mb = v
	}
	size := mb << 20
	f, body := bigFlowR3(t, st, size)
	const limit = 200_000
	pages := []int{1, 100, 1000}
	best := map[int]time.Duration{}
	for _, p := range pages {
		offset := (p - 1) * limit
		if offset >= size {
			offset = size - limit
		}
		for run := 0; run < 5; run++ {
			out, d := sliceAt(t, h, f.ID, offset, limit)
			if out.Text != string(body[offset:offset+limit]) || out.Offset != offset {
				t.Fatalf("page %d: wrong window (offset %d, %d bytes)", p, out.Offset, len(out.Text))
			}
			if cur, ok := best[p]; !ok || d < cur {
				best[p] = d
			}
		}
		t.Logf("%d MiB body, page %d (offset %d): best of 5 = %v", mb, p, offset, best[p])
	}
	if far, first := best[1000], best[1]; far > 4*first+3*time.Millisecond {
		t.Errorf("the last page took %v against %v for the first: the body is re-read from the start", far, first)
	}
}

// A window that starts inside a multi-byte rune still snaps to a rune
// boundary when the window is reached by seeking.
func TestFlowSliceSeekKeepsRuneBoundaries(t *testing.T) {
	h, st, _ := newHub(t)
	body := bytes.Repeat([]byte("héllo wörld ✓ "), 4000)
	hash, n := putBodyR2(t, st, body)
	f := &store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "app.example.com", Port: 443, Path: "/t",
		Status: 200, Mime: "text/plain", ResBodyHash: hash, ResLen: n}
	if _, err := st.InsertFlow(f); err != nil {
		t.Fatal(err)
	}
	var got []byte
	offset := 0
	for i := 0; i < 10000; i++ {
		out, _ := sliceAt(t, h, f.ID, offset, 777)
		got = append(got, out.Text...)
		if !out.More {
			break
		}
		offset = out.Next
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("paged body differs from the stored one (%d vs %d bytes)", len(got), len(body))
	}
}

// A compressed capture is decoded for display when it fits the display cap,
// and one whose stored bytes already exceed the cap is streamed as stored
// (what the in-memory path also ended up returning, after loading it whole).
func TestFlowRawCompressedBodies(t *testing.T) {
	h, st, _ := newHub(t)
	gz := func(plain []byte, level int) []byte {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, level)
		zw.Write(plain)
		zw.Close()
		return buf.Bytes()
	}
	insert := func(stored []byte) *store.Flow {
		hash, n := putBodyR2(t, st, stored)
		f := &store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "app.example.com", Port: 443, Path: "/z",
			Status: 200, Mime: "text/plain", ResHeaders: map[string][]string{"Content-Encoding": {"gzip"}},
			ResBodyHash: hash, ResLen: n}
		if _, err := st.InsertFlow(f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	raw := func(f *store.Flow, w http.ResponseWriter) {
		req := httptest.NewRequest(http.MethodGet, "/api/flows/x/raw?side=res", nil)
		req.SetPathValue("id", strconv.FormatInt(f.ID, 10))
		(&flowAPI{h}).getFlowRaw(w, req)
	}

	small := insert(gz([]byte("hello, decoded world"), gzip.BestSpeed))
	rec := httptest.NewRecorder()
	raw(small, rec)
	if got := rec.Body.String(); !strings.HasSuffix(got, "\r\n\r\nhello, decoded world") || !strings.Contains(got, "X-Interseptor-Decoded: gzip") {
		t.Fatalf("small gzip body is no longer decoded for display: %q", got)
	}

	stored := gz(bytes.Repeat([]byte{'x'}, decodeMax+(4<<20)), gzip.NoCompression) // stored > decodeMax
	big := insert(stored)
	var w *countingWriter
	alloc := allocatedBy(func() {
		w = &countingWriter{}
		raw(big, w)
	})
	t.Logf("gzip body stored at %d MiB: %d bytes written, %.1f MiB allocated", len(stored)>>20, w.n, float64(alloc)/(1<<20))
	if w.n < int64(len(stored)) || !bytes.Contains(w.head, []byte("Content-Encoding: gzip")) {
		t.Fatalf("oversized gzip body: wrote %d bytes, head %q; want the stored bytes with their Content-Encoding", w.n, w.head)
	}
	if alloc > 4<<20 {
		t.Errorf("allocated %d MiB to pass an oversized compressed body through", alloc>>20)
	}
}
