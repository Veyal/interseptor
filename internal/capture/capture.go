// Package capture streams HTTP bodies into the store as they pass through,
// without buffering them in memory.
package capture

import (
	"io"
	"net/http"

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
// commit the body and obtain its hash and length. If src is nil or http.NoBody,
// the returned reader is nil (keep forwarding src) and finalize reports no body.
func (c *Capturer) TeeBody(src io.Reader) (r io.Reader, finalize func() (string, int64, error), err error) {
	r, finalize, _, err = c.TeeBodyWithAbort(src)
	return r, finalize, err
}

// TeeBodyWithAbort is TeeBody with an explicit cleanup callback for callers
// that stop consuming src after a read error.
func (c *Capturer) TeeBodyWithAbort(src io.Reader) (r io.Reader, finalize func() (string, int64, error), abort func(), err error) {
	// http.NoBody is what net/http hands every server-side GET. Leaving it
	// unwrapped keeps the caller forwarding NoBody itself, so the outbound
	// Transport sends no body without probing a wrapped reader, and capture
	// does no work at all.
	if src == nil || src == http.NoBody {
		return nil, func() (string, int64, error) { return "", 0, nil }, func() {}, nil
	}
	w, err := c.st.NewFlowBodyWriter()
	if err != nil {
		return nil, nil, nil, err
	}
	return io.TeeReader(src, w), w.Finalize, w.Abort, nil
}
