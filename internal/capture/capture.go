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
	return io.TeeReader(src, w), w.Finalize, w.Abort, nil
}
