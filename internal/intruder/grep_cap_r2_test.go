package intruder

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// r2GrepEngine wires the bounded reader the way a store-backed caller would,
// and reports the largest slice any single grep was handed.
func r2GrepEngine(t *testing.T) (*Engine, *atomic.Int64) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := New(sender.New(st, capture.New(st)))
	t.Cleanup(e.Close)
	var biggest atomic.Int64
	e.SetBodyPrefixReader(func(hash string, max int64) ([]byte, bool, error) {
		rc, err := st.OpenBody(hash)
		if err != nil {
			return nil, false, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, max+1))
		if err != nil {
			return nil, false, err
		}
		truncated := int64(len(b)) > max
		if truncated {
			b = b[:max]
		}
		if n := int64(len(b)); n > biggest.Load() {
			biggest.Store(n)
		}
		return b, truncated, nil
	})
	return e, &biggest
}

func r2Run(t *testing.T, e *Engine, url string) Result {
	t.Helper()
	if err := e.Start(Spec{
		Target: url, Template: "GET / HTTP/1.1\nHost: example.com\n\n",
		AttackType: "repeat", Repeat: 1, Threads: 1, GrepMatch: "NEEDLE", GrepExtract: `tok=(\w+)`,
	}); err != nil {
		t.Fatal(err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("results = %+v", st.Results)
	}
	return st.Results[0]
}

// A body larger than the cap is grepped over its first MaxGrepBytes only, the
// reader is never asked for more, and the result says the grep was cut.
func TestR2GrepReadsAtMostTheCap(t *testing.T) {
	old := MaxGrepBytes
	MaxGrepBytes = 64 << 10
	defer func() { MaxGrepBytes = old }()

	body := "NEEDLE tok=early " + strings.Repeat("a", 512<<10) + " tok=late"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, body)
	}))
	defer upstream.Close()

	e, biggest := r2GrepEngine(t)
	res := r2Run(t, e, upstream.URL)
	if !res.Matched || res.Extracted != "early" {
		t.Errorf("content inside the cap must still be grepped: %+v", res)
	}
	if !res.GrepTruncated {
		t.Errorf("a grep cut at the cap must say so: %+v", res)
	}
	if got := biggest.Load(); got > int64(MaxGrepBytes) {
		t.Errorf("grep read %d bytes, cap is %d", got, MaxGrepBytes)
	}
}

func TestR2GrepUnderTheCapIsComplete(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("a", 100<<10)+" NEEDLE tok=late")
	}))
	defer upstream.Close()
	e, _ := r2GrepEngine(t)
	res := r2Run(t, e, upstream.URL)
	if !res.Matched || res.Extracted != "late" || res.GrepTruncated {
		t.Errorf("a body under the cap must be grepped whole and not flagged: %+v", res)
	}
}

// The legacy whole-body reader keeps working, with the same cap applied to
// what is grepped.
func TestR2LegacyReaderIsCappedToo(t *testing.T) {
	old := MaxGrepBytes
	MaxGrepBytes = 64 << 10
	defer func() { MaxGrepBytes = old }()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("a", 512<<10)+" NEEDLE")
	}))
	defer upstream.Close()
	e, _ := newEngineWithStore(t)
	res := r2Run(t, e, upstream.URL)
	if res.Matched || !res.GrepTruncated {
		t.Errorf("content past the cap must not match and the cut must be flagged: %+v", res)
	}
}

// A compressed body that inflates past the cap is grepped over the decoded
// prefix, not skipped and not grepped as compressed bytes.
func TestR2GrepCapsDecompressedSize(t *testing.T) {
	old := MaxGrepBytes
	MaxGrepBytes = 64 << 10
	defer func() { MaxGrepBytes = old }()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	io.WriteString(zw, "NEEDLE tok=early "+strings.Repeat("a", 512<<10))
	zw.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz.Bytes())
	}))
	defer upstream.Close()
	e, _ := r2GrepEngine(t)
	res := r2Run(t, e, upstream.URL)
	if !res.Matched || res.Extracted != "early" || !res.GrepTruncated {
		t.Errorf("decoded prefix must be grepped and flagged: %+v", res)
	}
}

// A binary response is never read at all.
func TestR2BinaryBodyIsNotRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(bytes.Repeat([]byte{0x89}, 256<<10))
	}))
	defer upstream.Close()
	e, biggest := r2GrepEngine(t)
	res := r2Run(t, e, upstream.URL)
	if !res.Binary || biggest.Load() != 0 {
		t.Errorf("binary body: result %+v, bytes read %d (want 0)", res, biggest.Load())
	}
}
