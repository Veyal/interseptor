package scriptworker

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// blockingErrReader serves the run frame, then blocks; after unblock it fails
// the next read with a non-EOF error (a parent protocol failure arriving late).
type blockingErrReader struct {
	frame []byte
	pos   int
	wait  chan struct{}
}

func (r *blockingErrReader) Read(p []byte) (int, error) {
	if r.pos < len(r.frame) {
		n := copy(p, r.frame[r.pos:])
		r.pos += n
		return n, nil
	}
	<-r.wait
	return 0, errors.New("late protocol failure")
}

// Serve's reader goroutine may still be running when Serve returns (the run is
// over, but stdin stays open). Its read error must not be a variable Serve
// reads unsynchronized: go test -race flags that, and a late error was missed.
func TestServeReaderErrorIsSynchronized(t *testing.T) {
	r := &blockingErrReader{frame: runFrame(t, nil), wait: make(chan struct{})}
	// Unblock from a goroutine created before Serve reads the error: closing
	// from the test goroutine after Serve returns would order the write after
	// the read and hide the race.
	go func() { time.Sleep(30 * time.Millisecond); close(r.wait) }()
	var out bytes.Buffer
	// Whether the late error arrives before or after Serve checks is timing; the
	// regression under -race is the unsynchronized access, not the value.
	_ = Serve(r, &out, WorkerConfig{})
	time.Sleep(120 * time.Millisecond) // let the reader goroutine finish writing
}
