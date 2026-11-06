package capture

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// failingWriter accepts a few bytes, then fails like a full disk.
type failingWriter struct {
	limit int
	total int
}

var errDiskFull = errors.New("no space left on device")

func (w *failingWriter) Write(p []byte) (int, error) {
	room := w.limit - w.total
	if room <= 0 {
		return 0, errDiskFull
	}
	if len(p) <= room {
		w.total += len(p)
		return len(p), nil
	}
	w.total += room
	return room, errDiskFull
}

// Capture is best-effort: a body that cannot be written must not abort the
// stream the client is reading, but it must not be committed as if complete
// either — Finalize has to report the failure so the caller can flag it.
func TestTeeReaderSurvivesWriteFailureAndFinalizeReportsIt(t *testing.T) {
	const body = "hello from the origin, this body will not fit on the disk"
	tee := &teeReader{src: strings.NewReader(body), dst: &failingWriter{limit: 5}}
	got, err := io.ReadAll(tee)
	if err != nil {
		t.Fatalf("tee aborted the stream on a capture write failure: %v", err)
	}
	if string(got) != body {
		t.Fatalf("stream = %q, want the whole body %q", got, body)
	}
	if tee.err == nil {
		t.Fatal("the tee must remember the write failure so Finalize can report it")
	}
	if !errors.Is(tee.err, errDiskFull) {
		t.Fatalf("remembered error = %v, want the write error", tee.err)
	}
}

// shortWriter accepts the call but writes fewer bytes than it was given.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestTeeReaderRecordsShortWrite(t *testing.T) {
	tee := &teeReader{src: strings.NewReader("abc"), dst: shortWriter{}}
	if _, err := io.ReadAll(tee); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !errors.Is(tee.err, io.ErrShortWrite) {
		t.Fatalf("tee error = %v, want io.ErrShortWrite", tee.err)
	}
}

// A clean stream keeps the tee error-free.
func TestTeeReaderCleanStreamHasNoError(t *testing.T) {
	var dst strings.Builder
	tee := &teeReader{src: strings.NewReader("abc"), dst: &dst}
	got, err := io.ReadAll(tee)
	if err != nil || string(got) != "abc" || tee.err != nil {
		t.Fatalf("read=%q err=%v teeErr=%v", got, err, tee.err)
	}
	if dst.String() != "abc" {
		t.Fatalf("tee copied %q, want abc", dst.String())
	}
}
