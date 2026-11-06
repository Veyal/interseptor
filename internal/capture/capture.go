// Package capture streams HTTP bodies into the store as they pass through,
// without buffering them in memory.
package capture

import (
	"io"

	"github.com/Veyal/interseptor/internal/store"
)

// Capturer creates body-capturing tees backed by a Store.
type Capturer struct {
	st *store.Store
}

// New returns a Capturer writing to st.
func New(st *store.Store) *Capturer { return &Capturer{st: st} }

// TeeBody wraps src so that every byte read from the returned reader is also
// written to the store. Call finalize after the reader is fully consumed to
// commit the body and obtain its hash and length. If src is nil, the returned
// reader is empty and finalize reports an empty body.
func (c *Capturer) TeeBody(src io.Reader) (r io.Reader, finalize func() (string, int64, error), err error) {
	r, finalize, _, err = c.TeeBodyWithAbort(src)
	return r, finalize, err
}

// TeeBodyWithAbort is TeeBody with an explicit cleanup callback for callers
// that stop consuming src after a read error.
func (c *Capturer) TeeBodyWithAbort(src io.Reader) (r io.Reader, finalize func() (string, int64, error), abort func(), err error) {
	if src == nil {
		return nil, func() (string, int64, error) { return "", 0, nil }, func() {}, nil
	}
	w, err := c.st.NewFlowBodyWriter()
	if err != nil {
		return nil, nil, nil, err
	}
	t := &teeReader{src: src, dst: w}
	// Finalize reports a write failure instead of returning a hash for a body
	// that is only partly on disk, so the caller can flag the capture.
	fin := func() (string, int64, error) {
		if t.err != nil {
			w.Abort()
			return "", 0, t.err
		}
		return w.Finalize()
	}
	return t, fin, w.Abort, nil
}

// teeReader copies what it reads into the capture writer, but a capture write
// failure is remembered instead of returned: capture is best-effort, so a full
// disk must never abort the response the client is waiting for. The remembered
// error makes Finalize fail rather than commit a truncated body as complete.
type teeReader struct {
	src io.Reader
	dst io.Writer
	err error
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 && t.err == nil {
		if n2, werr := t.dst.Write(p[:n]); werr != nil {
			t.err = werr
		} else if n2 < n {
			t.err = io.ErrShortWrite
		}
	}
	return n, err
}
