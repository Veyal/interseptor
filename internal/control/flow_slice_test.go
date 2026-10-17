package control

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func sliceFixture(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func insertResFlow(t *testing.T, st *store.Store, body []byte, hdr map[string][]string, mime string) int64 {
	t.Helper()
	hash := writeBody(t, st, string(body))
	f := &store.Flow{Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/big", Status: 200,
		Mime: mime, ResHeaders: hdr, ResBodyHash: hash, ResLen: int64(len(body))}
	id, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func getSlice(t *testing.T, ts *httptest.Server, id int64, query string) (int, flowSliceJSON, string) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/flows/%d/slice?%s", ts.URL, id, query))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out flowSliceJSON
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

// A 5 MB body made get_flow return only an error (the MCP bridge caps a reply
// at 4 MiB). The slice view always returns the head plus a bounded window.
func TestFlowSliceLargeBodyWindow(t *testing.T) {
	ts, st := sliceFixture(t)
	body := bytes.Repeat([]byte("0123456789"), 512*1024) // 5 MiB
	copy(body[3_000_000:], "NEEDLE")
	id := insertResFlow(t, st, body, map[string][]string{"Content-Type": {"text/plain"}}, "text/plain")

	code, out, raw := getSlice(t, ts, id, "side=res&limit=100")
	if code != 200 || !strings.HasPrefix(out.Head, "HTTP/1.1 200 OK\r\n") || !strings.Contains(out.Head, "Content-Type: text/plain") {
		t.Fatalf("head missing: %d %s", code, raw[:min(len(raw), 300)])
	}
	if out.BodyBytes != int64(len(body)) || out.Shown != 100 || out.Text != string(body[:100]) || !out.More || out.Next != 100 {
		t.Fatalf("window = %+v", out)
	}
	if len(raw) > 2000 {
		t.Fatalf("slice reply is %d bytes for a 100-byte window", len(raw))
	}
	_, out, _ = getSlice(t, ts, id, "side=res&offset=3000000&limit=6")
	if out.Text != "NEEDLE" || out.Offset != 3000000 {
		t.Fatalf("offset window = %q at %d", out.Text, out.Offset)
	}
	_, out, _ = getSlice(t, ts, id, "side=res&grep=NEEDLE&context=0")
	if out.Grep == nil || out.Grep.Count != 1 || len(out.Grep.Matches) != 1 || out.Grep.Matches[0].Offset != 3000000 {
		t.Fatalf("grep = %+v", out.Grep)
	}
	if len(out.Grep.Matches[0].Text) > sliceMaxLineRunes*4+8 {
		t.Fatalf("grep line not bounded: %d bytes", len(out.Grep.Matches[0].Text))
	}
}

func TestFlowSliceBinaryBodyIsDescribedNotDumped(t *testing.T) {
	ts, st := sliceFixture(t)
	body := make([]byte, 200*1024)
	for i := range body {
		body[i] = byte(i*7 + 3)
	}
	id := insertResFlow(t, st, body, map[string][]string{"Content-Type": {"application/octet-stream"}}, "application/octet-stream")
	_, out, raw := getSlice(t, ts, id, "side=res")
	if !out.Binary || out.Text != "" || out.HexHead == "" || out.BodyBytes != int64(len(body)) {
		t.Fatalf("binary body = %s", raw[:min(len(raw), 400)])
	}
	if len(raw) > 4000 {
		t.Fatalf("binary description is %d bytes", len(raw))
	}
}

func TestFlowSliceRuneBoundariesAndGzip(t *testing.T) {
	ts, st := sliceFixture(t)
	text := strings.Repeat("é", 100) + "\nline two has token=abc\nline three"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(text))
	_ = zw.Close()
	id := insertResFlow(t, st, gz.Bytes(), map[string][]string{"Content-Encoding": {"gzip"}, "Content-Type": {"text/plain"}}, "text/plain")

	_, out, raw := getSlice(t, ts, id, "side=res&offset=1&limit=5")
	if !out.Decoded || strings.Contains(out.Head, "Content-Encoding") {
		t.Fatalf("gzip body not decoded for display: %s", raw)
	}
	if !strings.HasPrefix(out.Text, "é") || !json.Valid([]byte(raw)) || strings.ContainsRune(out.Text, '�') {
		t.Fatalf("window split a rune: %q (offset %d)", out.Text, out.Offset)
	}
	_, out, _ = getSlice(t, ts, id, "side=res&grep=token%3D&context=1")
	if out.Grep == nil || out.Grep.Count != 1 || out.Grep.Matches[0].Line != 2 || !strings.Contains(out.Grep.Matches[0].Text, "line three") {
		t.Fatalf("grep with context = %+v", out.Grep)
	}
}

func TestFlowSliceRejectsBadInput(t *testing.T) {
	ts, st := sliceFixture(t)
	id := insertResFlow(t, st, []byte("hi"), nil, "text/plain")
	for _, q := range []string{"limit=-1", "offset=x", "grep=(", "context=99", "maxMatches=0"} {
		if code, _, raw := getSlice(t, ts, id, q); code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", q, code, raw)
		}
	}
	if code, _, _ := getSlice(t, ts, 999, ""); code != http.StatusNotFound {
		t.Fatalf("missing flow = %d", code)
	}
}
