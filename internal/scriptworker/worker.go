package scriptworker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

// WorkerConfig tunes Serve.
type WorkerConfig struct {
	// OnMemoryBreach is called (after a best-effort FrameError) when the
	// worker's own memory use crosses the ceiling from the run request. The
	// real worker exits the process; tests may set a recorder. nil = no-op.
	OnMemoryBreach func()
	// PollInterval is the self-watchdog period (default 10ms).
	PollInterval time.Duration
}

// Serve is the worker side: it reads one run request from r, executes it with
// pmsandbox, and writes the output to w. pm.sendRequest and scope checks are
// proxied to the parent over the same pipes. It returns when the run is done,
// the parent closes the pipe, or the protocol is violated.
func Serve(r io.Reader, w io.Writer, cfg WorkerConfig) error {
	br := bufio.NewReaderSize(r, 64<<10)
	typ, payload, err := ReadFrame(br)
	if err != nil {
		return fmt.Errorf("scriptworker: read run: %w", err)
	}
	if typ != FrameRun {
		return fmt.Errorf("%w: expected run frame", ErrBadFrame)
	}
	var run runWire
	if err := json.Unmarshal(payload, &run); err != nil {
		return fmt.Errorf("scriptworker: decode run: %w", err)
	}
	if run.V != ProtocolVersion {
		return fmt.Errorf("scriptworker: protocol version %d unsupported", run.V)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ep := &endpoint{w: w, pending: map[uint64]chan replyWire{}}

	var readErr error
	readDone := make(chan struct{})
	go func() { // demux replies and cancellation; EOF means the parent is gone
		defer close(readDone)
		defer cancel()
		for {
			t, p, err := ReadFrame(br)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				ep.failAll()
				return
			}
			switch t {
			case FrameReply:
				var rp replyWire
				if json.Unmarshal(p, &rp) == nil {
					ep.deliver(rp)
				}
			case FrameCancel:
				ep.failAll()
				return
			default:
				readErr = fmt.Errorf("%w: unexpected 0x%02x from parent", ErrBadFrame, t)
				ep.failAll()
				return
			}
		}
	}()

	if run.MemLimit > 0 {
		stop := watchMemory(ctx, run.MemLimit, cfg.PollInterval, func() {
			b, _ := json.Marshal(errWire{Kind: KindMemory, Message: "script exceeded the memory limit"})
			ep.write(FrameError, b)
			if cfg.OnMemoryBreach != nil {
				cfg.OnMemoryBreach()
			}
			cancel()
		})
		defer stop()
	}

	in := pmsandbox.Input{
		Phase: run.Phase, Name: run.Name, Script: run.Script, Request: run.Request,
		Response: run.response(), Vars: run.Vars, Cookies: run.Cookies, Info: run.Info,
		Caps: run.Caps, Limits: run.Limits,
		ScopeCheck: func(u string) error { return ep.scope(ctx, u) },
	}
	if run.ClockNanos != 0 {
		t := time.Unix(0, run.ClockNanos)
		in.Clock = func() time.Time { return t }
	}
	if run.HasSeed {
		in.Rand = rand.New(rand.NewPCG(run.RandSeed, run.RandSeed^0x9e3779b97f4a7c15)).Float64
	}
	if run.HasSend {
		in.Sender = scriptctx.SendFunc(func(c context.Context, req scriptctx.SendRequest) (scriptctx.SendResponse, error) {
			return ep.send(c, req)
		})
	}

	out := pmsandbox.Run(ctx, in)
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("scriptworker: encode output: %w", err)
	}
	if err := ep.write(FrameOutput, b); err != nil {
		return err
	}
	cancel()
	if readErr != nil {
		return readErr
	}
	return nil
}

// endpoint is the worker's RPC client to the parent.
type endpoint struct {
	wmu     sync.Mutex
	w       io.Writer
	mu      sync.Mutex
	next    uint64
	pending map[uint64]chan replyWire
	closed  bool
}

func (e *endpoint) write(t byte, p []byte) error {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	return WriteFrame(e.w, t, p)
}

func (e *endpoint) register() (uint64, chan replyWire, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, nil, errors.New("scriptworker: parent connection closed")
	}
	e.next++
	ch := make(chan replyWire, 1)
	e.pending[e.next] = ch
	return e.next, ch, nil
}

func (e *endpoint) deliver(r replyWire) {
	e.mu.Lock()
	ch := e.pending[r.ID]
	delete(e.pending, r.ID)
	e.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

func (e *endpoint) failAll() {
	e.mu.Lock()
	e.closed = true
	for id, ch := range e.pending {
		delete(e.pending, id)
		close(ch)
	}
	e.mu.Unlock()
}

func (e *endpoint) call(ctx context.Context, t byte, c callWire) (replyWire, error) {
	id, ch, err := e.register()
	if err != nil {
		return replyWire{}, err
	}
	c.ID = id
	b, _ := json.Marshal(c)
	if err := e.write(t, b); err != nil {
		return replyWire{}, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return replyWire{}, errors.New("scriptworker: parent connection closed")
		}
		if r.Err != "" {
			return r, errors.New(r.Err)
		}
		return r, nil
	case <-ctx.Done():
		return replyWire{}, ctx.Err()
	}
}

func (e *endpoint) scope(ctx context.Context, url string) error {
	_, err := e.call(ctx, FrameScope, callWire{URL: url})
	return err
}

func (e *endpoint) send(ctx context.Context, req scriptctx.SendRequest) (scriptctx.SendResponse, error) {
	r, err := e.call(ctx, FrameSend, callWire{Send: &req})
	if err != nil {
		return scriptctx.SendResponse{}, err
	}
	if r.Resp == nil {
		return scriptctx.SendResponse{}, errors.New("scriptworker: empty send reply")
	}
	return scriptctx.SendResponse{
		Code: r.Resp.Code, Status: r.Resp.Status, Headers: r.Resp.Headers,
		Body: r.Resp.Body, ResponseTime: time.Duration(r.Resp.ResponseNs),
	}, nil
}

var memMetrics = []string{"/memory/classes/total:bytes", "/memory/classes/heap/released:bytes"}

// memInUse is Go's view of memory held by this process (mapped minus
// released to the OS), a close proxy for RSS that is cheap to read.
func memInUse() uint64 {
	s := []metrics.Sample{{Name: memMetrics[0]}, {Name: memMetrics[1]}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	t, rel := s[0].Value.Uint64(), s[1].Value.Uint64()
	if rel > t {
		return 0
	}
	return t - rel
}

func watchMemory(ctx context.Context, limit uint64, every time.Duration, onBreach func()) (stop func()) {
	if every <= 0 {
		every = 10 * time.Millisecond
	}
	done := make(chan struct{})
	base := memInUse()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if cur := memInUse(); cur > base && cur > limit {
					onBreach()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}
