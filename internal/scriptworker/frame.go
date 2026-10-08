// Package scriptworker runs untrusted collection scripts out of process.
//
// The parent re-execs the interseptor binary as `interseptor __scriptworker`
// and talks to it over stdin/stdout with length-prefixed frames. goja has no
// heap cap, so a memory bomb or crash in a script kills only the worker: the
// parent enforces a wall-clock deadline and an RSS ceiling, kills the child on
// breach, and reports a script error. The worker never dials anything itself;
// pm.sendRequest and the scope check are proxied back to the parent, which
// re-checks scope before sending.
//
// Executor is the swap point: InProcess runs pmsandbox directly (trusted
// scripts, per policy), Subprocess runs it in a worker.
package scriptworker

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame types. A frame is: 4-byte big-endian payload length, 1 type byte,
// then the payload (JSON for every type).
const (
	FrameRun    byte = 'R' // parent -> worker: run request
	FrameReply  byte = 'P' // parent -> worker: reply to a send or scope call
	FrameCancel byte = 'X' // parent -> worker: abort the run
	FrameSend   byte = 'S' // worker -> parent: pm.sendRequest
	FrameScope  byte = 'C' // worker -> parent: scope check
	FrameOutput byte = 'O' // worker -> parent: final output
	FrameError  byte = 'E' // worker -> parent: fatal worker error
)

// MaxFrame caps one frame payload (32 MiB). Larger frames are a protocol
// error and end the session.
const MaxFrame = 32 << 20

// ErrFrameTooLarge is returned for a frame whose declared length exceeds the cap.
var ErrFrameTooLarge = errors.New("scriptworker: frame too large")

// ErrBadFrame is returned for an unknown frame type.
var ErrBadFrame = errors.New("scriptworker: bad frame type")

func validType(t byte) bool {
	switch t {
	case FrameRun, FrameReply, FrameCancel, FrameSend, FrameScope, FrameOutput, FrameError:
		return true
	}
	return false
}

// WriteFrame writes one frame. It is not safe for concurrent use on one
// writer; callers serialise with a mutex.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return ErrFrameTooLarge
	}
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(payload)))
	hdr[4] = typ
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one frame. The payload buffer grows with data actually
// received, so a lying length prefix cannot force a large allocation.
func ReadFrame(r *bufio.Reader) (typ byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	typ = hdr[4]
	if n > MaxFrame {
		return 0, nil, ErrFrameTooLarge
	}
	if !validType(typ) {
		return 0, nil, fmt.Errorf("%w: 0x%02x", ErrBadFrame, typ)
	}
	buf := make([]byte, 0, min(int(n), 64<<10))
	for len(buf) < int(n) {
		chunk := min(int(n)-len(buf), 64<<10)
		start := len(buf)
		buf = append(buf, make([]byte, chunk)...)
		if _, err = io.ReadFull(r, buf[start:]); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, nil, err
		}
	}
	return typ, buf, nil
}
