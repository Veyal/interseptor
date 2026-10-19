package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A held response whose body is not text (a PNG) cannot survive a round trip
// through the JSON string and the editor textarea: invalid UTF-8 becomes U+FFFD
// and every CR becomes LF. The raw endpoint therefore sends only the head and
// marks the message binary, and Forward with keepBody splices the edited head
// onto the original body bytes.
var pngBody = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\xff\xfe\r\x00")

func holdBinaryResponse(t *testing.T, h *Hub, path string) (id int64, raw []byte, done chan []byte) {
	t.Helper()
	h.eng.SetResponseEnabled(true)
	raw = append([]byte("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: "+strconv.Itoa(len(pngBody))+"\r\n\r\n"), pngBody...)
	done = make(chan []byte, 1)
	go func() {
		d := h.eng.HoldResponse(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: path}, raw)
		done <- d.Raw
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if q := h.eng.ResponseQueue(); len(q) == 1 {
			return q[0].ID, raw, done
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("response never held")
	return 0, nil, nil
}

func TestHeldRawMarksBinaryBodyAndSendsHeadOnly(t *testing.T) {
	h, _, _ := newHub(t)
	id, raw, done := holdBinaryResponse(t, h, "/img.png")
	t.Cleanup(func() { _ = h.eng.DropResponse(id); <-done })

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/intercept/held/" + strconv.FormatInt(id, 10) + "/raw?side=resp")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Raw         string `json:"raw"`
		Binary      bool   `json:"binary"`
		BodyLen     int    `json:"bodyLen"`
		ContentType string `json:"contentType"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	head := string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))+4])
	if !out.Binary || out.BodyLen != len(pngBody) || out.Raw != head || out.ContentType != "image/png" {
		t.Fatalf("held raw = %+v, want binary head-only %q with bodyLen %d", out, head, len(pngBody))
	}
}

func TestHeldRawKeepsTextBodies(t *testing.T) {
	h, _, eng := newHub(t)
	eng.SetEnabled(true)
	req, _, flow := newHeldRequest(t, "/json")
	raw := []byte("POST /json HTTP/1.1\r\nHost: example.com\r\nContent-Length: 13\r\n\r\n{\"a\":\"héllo\"}")
	go eng.Hold(flow, req, raw)
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID
	t.Cleanup(func() { _ = eng.Drop(id) })
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/intercept/held/" + strconv.FormatInt(id, 10) + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if out["raw"] != string(raw) || out["binary"] != nil {
		t.Fatalf("text held raw = %v, want the full message and no binary flag", out)
	}
}

func TestForwardKeepBodySplicesEditedHeadOntoOriginalBytes(t *testing.T) {
	h, _, _ := newHub(t)
	id, _, done := holdBinaryResponse(t, h, "/img.png")
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	// The editor holds the LF-normalized head plus whatever the textarea had.
	edited := "HTTP/1.1 200 OK\nContent-Type: image/png\nX-Edited: 1\n\nignored text"
	body, _ := json.Marshal(map[string]any{"raw": edited, "keepBody": true})
	res, err := http.Post(ts.URL+"/api/intercept/response/"+strconv.FormatInt(id, 10)+"/forward", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("forward status = %d", res.StatusCode)
	}
	got := <-done
	want := append([]byte("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nX-Edited: 1\r\n\r\n"), pngBody...)
	if !bytes.Equal(got, want) {
		t.Fatalf("forwarded %q, want %q", got, want)
	}
}

func TestHeldBodyServesImageBytes(t *testing.T) {
	h, _, _ := newHub(t)
	id, _, done := holdBinaryResponse(t, h, "/img.png")
	t.Cleanup(func() { _ = h.eng.DropResponse(id); <-done })
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/intercept/held/" + strconv.FormatInt(id, 10) + "/body?side=resp")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if !bytes.Equal(got, pngBody) || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("held body = %q (%s, %v)", got, res.Header.Get("Content-Type"), res.Header)
	}
}

// A held body that is not a raster image is never served with its own type:
// an HTML body opened from this URL must not run in the control origin.
func TestHeldBodyNeverServesActiveContent(t *testing.T) {
	h, _, _ := newHub(t)
	h.eng.SetResponseEnabled(true)
	raw := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<script>alert(1)</script>")
	done := make(chan struct{})
	go func() { h.eng.HoldResponse(&store.Flow{Host: "example.com", Path: "/x"}, raw); close(done) }()
	for i := 0; i < 400 && len(h.eng.ResponseQueue()) == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	id := h.eng.ResponseQueue()[0].ID
	t.Cleanup(func() { _ = h.eng.DropResponse(id); <-done })
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/intercept/held/" + strconv.FormatInt(id, 10) + "/body?side=resp")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("html held body served as %q / %q", ct, res.Header.Get("Content-Disposition"))
	}
}
