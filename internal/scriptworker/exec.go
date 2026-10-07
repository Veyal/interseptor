package scriptworker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

// Executor runs one script phase. Implementations never panic and never
// return a Go error: failures land in Output (Status error, Errors[].Kind).
type Executor interface {
	Run(ctx context.Context, in pmsandbox.Input) pmsandbox.Output
}

// InProcess runs pmsandbox in the calling process. It is for trusted scripts
// only: goja has no heap cap beyond pmsandbox's growth watchdog.
type InProcess struct{}

// Run implements Executor.
func (InProcess) Run(ctx context.Context, in pmsandbox.Input) pmsandbox.Output {
	return pmsandbox.Run(ctx, in)
}

// Router picks the executor per policy: untrusted (quarantined, imported,
// AI-sourced) scripts always go to the worker; trusted scripts go in-process
// only when InProcessTrusted is set.
type Router struct {
	Worker           Executor
	Local            Executor // defaults to InProcess{}
	InProcessTrusted bool
}

// For returns the executor for a script's trust state.
func (r Router) For(trusted bool) Executor {
	if trusted && r.InProcessTrusted {
		if r.Local != nil {
			return r.Local
		}
		return InProcess{}
	}
	return r.Worker
}

// Run executes with the executor chosen for trusted.
func (r Router) Run(ctx context.Context, trusted bool, in pmsandbox.Input) pmsandbox.Output {
	return r.For(trusted).Run(ctx, in)
}

// Subprocess runs each script in a freshly spawned worker process and kills it
// on timeout, RSS breach, cancellation or protocol violation.
type Subprocess struct {
	// Path and Args start the worker. Default: the running binary with
	// "__scriptworker".
	Path string
	Args []string
	// Env is the worker environment. The parent environment is NOT inherited
	// (so secrets in env never reach untrusted scripts); GOMEMLIMIT is added.
	Env []string
	// MaxRSS is the resident-memory ceiling in bytes (default 512 MiB).
	MaxRSS uint64
	// Grace is extra wall time beyond the script timeout before the kill
	// (default 3s: covers startup and output marshalling).
	Grace time.Duration
	// PollInterval is the RSS poll period (default 100ms).
	PollInterval time.Duration
}

const defaultMaxRSS = 512 << 20

func (s Subprocess) command() (string, []string, error) {
	path, args := s.Path, s.Args
	if path == "" {
		p, err := os.Executable()
		if err != nil {
			return "", nil, err
		}
		path = p
		if args == nil {
			args = []string{"__scriptworker"}
		}
	}
	return path, args, nil
}

// effectiveTimeout mirrors pmsandbox's timeout defaults.
func effectiveTimeout(in pmsandbox.Input) time.Duration {
	t := in.Limits.Timeout
	if t <= 0 {
		t = jsrt.DefaultTimeout
		if in.Caps.NetSend {
			t = 30 * time.Second
		}
	}
	return min(t, jsrt.MaxTimeout)
}

func failOutput(in pmsandbox.Input, kind, msg string) pmsandbox.Output {
	return pmsandbox.Output{
		Status: pmsandbox.StatusError,
		Vars:   in.Vars,
		Errors: []pmsandbox.ScriptError{{Kind: kind, Message: msg}},
	}
}

// Run implements Executor.
func (s Subprocess) Run(ctx context.Context, in pmsandbox.Input) (out pmsandbox.Output) {
	begin := time.Now()
	defer func() {
		if p := recover(); p != nil {
			out = failOutput(in, KindCrash, "script worker supervisor panic")
		}
		out.Elapsed = time.Since(begin)
	}()
	maxRSS := s.MaxRSS
	if maxRSS == 0 {
		maxRSS = defaultMaxRSS
	}
	grace := s.Grace
	if grace <= 0 {
		grace = 3 * time.Second
	}
	path, args, err := s.command()
	if err != nil {
		return failOutput(in, KindCrash, "script worker unavailable: "+err.Error())
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.Command(path, args...)
	cmd.Env = append(append([]string{}, s.Env...), "GOMEMLIMIT="+strconv.FormatUint(maxRSS*3/4, 10))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return failOutput(in, KindCrash, "script worker unavailable: "+err.Error())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failOutput(in, KindCrash, "script worker unavailable: "+err.Error())
	}
	if err := cmd.Start(); err != nil {
		return failOutput(in, KindCrash, "script worker unavailable: "+err.Error())
	}

	sess := &session{in: in, stdin: stdin, ctx: runCtx, done: make(chan struct{})}
	var killReason struct {
		sync.Mutex
		kind, msg string
	}
	kill := func(kind, msg string) {
		killReason.Lock()
		if killReason.kind == "" {
			killReason.kind, killReason.msg = kind, msg
		}
		killReason.Unlock()
		_ = cmd.Process.Kill()
	}

	go sess.read(bufio.NewReaderSize(stdout, 64<<10))

	wire := toRunWire(in, maxRSS)
	b, _ := json.Marshal(wire)
	if err := sess.write(FrameRun, b); err != nil {
		kill(KindCrash, "script worker rejected the run request")
	}

	deadline := time.NewTimer(effectiveTimeout(in) + grace)
	defer deadline.Stop()
	poll := s.PollInterval
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
loop:
	for {
		select {
		case <-sess.done:
			break loop
		case <-deadline.C:
			kill(KindTimeout, "script worker exceeded the wall-clock limit and was killed")
			break loop
		case <-runCtx.Done():
			kill("canceled", "script canceled")
			break loop
		case <-tick.C:
			if rss := processRSS(cmd.Process.Pid); rss > maxRSS {
				kill(KindMemory, fmt.Sprintf("script worker exceeded the memory limit (%d MiB) and was killed", maxRSS>>20))
				break loop
			}
		}
	}
	_ = stdin.Close()
	cancel()
	waitErr := waitBounded(cmd)

	sess.mu.Lock()
	res, werr, proto := sess.result, sess.workerErr, sess.protoErr
	sess.mu.Unlock()
	killReason.Lock()
	kk, km := killReason.kind, killReason.msg
	killReason.Unlock()

	switch {
	case res != nil && kk == "":
		o := *res
		scrubOutput(&o, in.Scrub)
		return o
	case kk != "":
		return failOutput(in, kk, km)
	case werr != nil:
		return failOutput(in, werr.Kind, werr.Message)
	case proto != nil:
		return failOutput(in, KindProto, "script worker protocol error: "+proto.Error())
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) && ee.ExitCode() == ExitMemory {
		return failOutput(in, KindMemory, "script worker exceeded the memory limit")
	}
	return failOutput(in, KindCrash, "script worker exited without a result")
}

// waitBounded reaps the child, force-killing it if it lingers.
func waitBounded(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		return <-done
	}
}

// session is the parent end of one worker conversation.
type session struct {
	in    pmsandbox.Input
	ctx   context.Context
	wmu   sync.Mutex
	stdin io.Writer
	done  chan struct{}
	once  sync.Once

	mu        sync.Mutex
	result    *pmsandbox.Output
	workerErr *errWire
	protoErr  error
	sends     int
}

func (s *session) write(t byte, p []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return WriteFrame(s.stdin, t, p)
}

func (s *session) finish() { s.once.Do(func() { close(s.done) }) }

func (s *session) read(r *bufio.Reader) {
	defer s.finish()
	for {
		t, p, err := ReadFrame(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) {
				s.mu.Lock()
				if s.result == nil && s.protoErr == nil {
					s.protoErr = err
				}
				s.mu.Unlock()
			}
			return
		}
		switch t {
		case FrameOutput:
			var o pmsandbox.Output
			s.mu.Lock()
			if err := json.Unmarshal(p, &o); err != nil {
				s.protoErr = err
			} else {
				s.result = &o
			}
			s.mu.Unlock()
			return
		case FrameError:
			var e errWire
			if json.Unmarshal(p, &e) == nil {
				s.mu.Lock()
				s.workerErr = &e
				s.mu.Unlock()
			}
			return
		case FrameSend, FrameScope:
			var c callWire
			if err := json.Unmarshal(p, &c); err != nil {
				s.mu.Lock()
				s.protoErr = err
				s.mu.Unlock()
				return
			}
			go s.serve(t, c)
		default:
			s.mu.Lock()
			s.protoErr = fmt.Errorf("%w: unexpected 0x%02x from worker", ErrBadFrame, t)
			s.mu.Unlock()
			return
		}
	}
}

func (s *session) reply(r replyWire) {
	b, _ := json.Marshal(r)
	_ = s.write(FrameReply, b)
}

// serve answers a send or scope call. The parent re-checks scope before every
// send itself: the worker is not trusted to have done it.
func (s *session) serve(t byte, c callWire) {
	r := replyWire{ID: c.ID}
	defer func() {
		if p := recover(); p != nil {
			r = replyWire{ID: c.ID, Err: "internal error"}
		}
		s.reply(r)
	}()
	scope := func(u string) error {
		if s.in.ScopeCheck == nil {
			return nil
		}
		return s.in.ScopeCheck(u)
	}
	if t == FrameScope {
		if err := scope(c.URL); err != nil {
			r.Err = err.Error()
		}
		return
	}
	if c.Send == nil || s.in.Sender == nil {
		r.Err = "sending is not available"
		return
	}
	limit := s.in.Limits.MaxSends
	if limit <= 0 {
		limit = 25
	}
	s.mu.Lock()
	s.sends++
	over := s.sends > limit
	s.mu.Unlock()
	if over {
		r.Err = "too many requests from script"
		return
	}
	if err := scope(c.Send.URL); err != nil {
		r.Err = err.Error()
		return
	}
	resp, err := s.in.Sender.Send(s.ctx, *c.Send)
	if err != nil {
		r.Err = err.Error()
		return
	}
	if mr := s.in.Limits.MaxResponse; mr > 0 && len(resp.Body) > mr {
		resp.Body = resp.Body[:mr]
	}
	r.Resp = &sendRespWire{
		Code: resp.Code, Status: resp.Status, Headers: resp.Headers, Body: resp.Body,
		ResponseNs: int64(resp.ResponseTime),
	}
}

// scrubOutput applies the caller's scrubber to every human-facing string that
// crosses back from the worker. (Vars/Changes/Request stay raw by contract.)
func scrubOutput(o *pmsandbox.Output, scrub func(string) string) {
	if scrub == nil {
		return
	}
	for i := range o.Tests {
		t := &o.Tests[i]
		t.Name, t.Message, t.Expected, t.Actual = scrub(t.Name), scrub(t.Message), scrub(t.Expected), scrub(t.Actual)
	}
	for i := range o.Console {
		o.Console[i].Text = scrub(o.Console[i].Text)
	}
	for i := range o.Errors {
		o.Errors[i].Message, o.Errors[i].Stack = scrub(o.Errors[i].Message), scrub(o.Errors[i].Stack)
	}
	for i := range o.Unsupported {
		o.Unsupported[i] = scrub(o.Unsupported[i])
	}
}

var _ scriptctx.Sender = scriptctx.SendFunc(nil)
