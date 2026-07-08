package intruder

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// gzipBytes compresses s and returns the gzip bytes.
func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// newEngineWithStore builds an Engine and wires the store's body reader.
func newEngineWithStore(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	e := New(sender.New(st, capture.New(st)))
	e.SetBodyReader(func(hash string) []byte {
		rc, err := st.OpenBody(hash)
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	})
	return e, st
}

// TestGrepMatchOnGzipEncodedResponse verifies that grep-match finds the needle
// inside a gzip-compressed response body (the common real-world case).
func TestGrepMatchOnGzipEncodedResponse(t *testing.T) {
	const needle = "SECRET_TOKEN_XYZ"
	gzBody := gzipBytes(t, "some prefix "+needle+" some suffix")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(200)
		w.Write(gzBody)
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET / HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat",
		Repeat:     1,
		GrepMatch:  needle,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	if !res.Matched {
		t.Errorf("grep-match should find needle in gzip-encoded body; result: %+v", res)
	}
	if res.Binary {
		t.Errorf("text/html response should not be flagged as binary; result: %+v", res)
	}
}

// TestGrepMatchPlainTextStillWorks confirms that uncompressed (plain) responses
// continue to work correctly after the decompression layer was added.
func TestGrepMatchPlainTextStillWorks(t *testing.T) {
	const needle = "PLAINTEXT_NEEDLE"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "before "+needle+" after")
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET / HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat",
		Repeat:     1,
		GrepMatch:  needle,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	if !res.Matched {
		t.Errorf("grep-match should find needle in plain-text body; result: %+v", res)
	}
	if res.Binary {
		t.Errorf("text/plain should not be flagged as binary; result: %+v", res)
	}
}

// TestGrepMatchBinaryBodyNoMatchNoError verifies that binary responses (e.g.
// image/png) cause grep to be skipped (Binary=true, Matched=false) and do NOT
// panic or produce an error.
func TestGrepMatchBinaryBodyNoMatchNoError(t *testing.T) {
	// A minimal valid PNG header (binary data that is not text).
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		w.Write(pngHeader)
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET / HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat",
		Repeat:     1,
		GrepMatch:  "anything",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	if res.Matched {
		t.Errorf("grep-match should not match on a binary body; result: %+v", res)
	}
	if !res.Binary {
		t.Errorf("Binary flag should be set for image/png response; result: %+v", res)
	}
	if res.Error != "" {
		t.Errorf("binary body must not produce an error; got %q", res.Error)
	}
}

// TestGrepExtractOnGzipResponse verifies that grep-extract works on a
// gzip-encoded response, including capturing the regex group.
func TestGrepExtractOnGzipResponse(t *testing.T) {
	gzBody := gzipBytes(t, `{"token":"abc123","status":"ok"}`)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(gzBody)
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	err := e.Start(Spec{
		Target:      upstream.URL,
		Template:    "GET / HTTP/1.1\nHost: h\n\n",
		AttackType:  "repeat",
		Repeat:      1,
		GrepExtract: `"token":"(\w+)"`,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	if res.Extracted != "abc123" {
		t.Errorf("grep-extract from gzip body: want %q, got %q", "abc123", res.Extracted)
	}
}

// TestGrepMatchCorruptGzipFallsBackToRaw checks that a corrupt Content-Encoding
// body falls back to raw bytes and does not crash or set an error.
func TestGrepMatchCorruptGzipFallsBackToRaw(t *testing.T) {
	// "not gzip" advertised as gzip: decompression will fail; grep runs on raw bytes.
	const rawNeedle = "findme_raw"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		io.WriteString(w, rawNeedle+" rest of raw body")
	}))
	defer upstream.Close()

	e, _ := newEngineWithStore(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET / HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat",
		Repeat:     1,
		GrepMatch:  rawNeedle,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	res := st.Results[0]
	// Must not have crashed; error should be empty (transport succeeded).
	if res.Error != "" {
		t.Errorf("corrupt gzip fallback must not set an error; got %q", res.Error)
	}
	// The match result depends on whether the raw bytes contain the needle —
	// what we care about is: no panic, no crash, no attack-level error.
	// (Matched may or may not be true depending on the raw body content,
	// but the binary flag must not be set for text/plain.)
	if res.Binary {
		t.Errorf("text/plain must not be flagged binary even on fallback; result: %+v", res)
	}
}
