package jsrt

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

type timer struct {
	id       int64
	at       time.Duration // virtual due time
	seq      int64
	fn       goja.Callable
	args     []goja.Value
	interval time.Duration
	repeat   bool
}

type gojaRT struct {
	o       Options
	vm      *goja.Runtime
	closed  bool
	started time.Time
	virtual time.Duration // virtual time advanced by timers

	conMu      sync.Mutex
	con        []ConsoleEntry
	conBytes   int
	conTrunc   bool
	timers     []*timer
	nextTimer  int64
	timerCount int
	stringify  goja.Callable // pristine JSON.stringify captured before user code runs
}

func newGoja(o Options) *gojaRT {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Timeout > MaxTimeout {
		o.Timeout = MaxTimeout
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.MaxSource <= 0 {
		o.MaxSource = DefaultMaxSource
	}
	if o.ConsoleMax <= 0 {
		o.ConsoleMax = DefaultConsoleMax
	}
	if o.StackDepth <= 0 {
		o.StackDepth = DefaultStackDepth
	}
	if o.MaxTimers <= 0 {
		o.MaxTimers = DefaultMaxTimers
	}
	if o.VirtualSpan <= 0 {
		o.VirtualSpan = DefaultVirtualSpan
	}
	r := &gojaRT{o: o, vm: goja.New()}
	r.vm.SetMaxCallStackSize(o.StackDepth)
	r.vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	base := o.Clock()
	r.started = base
	r.vm.SetTimeSource(func() time.Time { return base.Add(r.virtual) })
	if o.Rand != nil {
		r.vm.SetRandSource(o.Rand)
	}
	if j, ok := r.vm.Get("JSON").(*goja.Object); ok {
		r.stringify, _ = goja.AssertFunction(j.Get("stringify"))
	}
	r.installConsole()
	r.installTimers()
	r.installPerformance()
	return r
}

func (r *gojaRT) Close() { r.closed = true }

func (r *gojaRT) Set(name string, v any) error {
	if r.closed {
		return errors.New("jsrt: runtime closed")
	}
	switch x := v.(type) {
	case Func:
		return r.vm.Set(name, r.wrapFunc(x))
	case map[string]any:
		return r.vm.Set(name, r.convertMap(x))
	}
	return r.vm.Set(name, v)
}

func (r *gojaRT) convertMap(m map[string]any) *goja.Object {
	o := r.vm.NewObject()
	for k, v := range m {
		switch x := v.(type) {
		case Func:
			_ = o.Set(k, r.wrapFunc(x))
		case map[string]any:
			_ = o.Set(k, r.convertMap(x))
		default:
			_ = o.Set(k, v)
		}
	}
	return o
}

func (r *gojaRT) wrapFunc(f Func) func(goja.FunctionCall) goja.Value {
	return func(c goja.FunctionCall) goja.Value {
		args := make([]any, len(c.Arguments))
		for i, a := range c.Arguments {
			args[i] = a.Export()
		}
		out, err := f(args)
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		return r.vm.ToValue(out)
	}
}

func (r *gojaRT) installConsole() {
	con := r.vm.NewObject()
	for _, lvl := range []string{"log", "info", "warn", "error", "debug"} {
		lvl := lvl
		_ = con.Set(lvl, func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for i, a := range c.Arguments {
				parts[i] = r.fmtValue(a)
			}
			r.addConsole(lvl, strings.Join(parts, " "))
			return goja.Undefined()
		})
	}
	_ = r.vm.Set("console", con)
}

func (r *gojaRT) fmtValue(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if o, ok := v.(*goja.Object); ok && r.stringify != nil {
		if out, err := r.stringify(goja.Undefined(), o); err == nil && !goja.IsUndefined(out) {
			return out.String()
		}
	}
	return v.String()
}

func (r *gojaRT) addConsole(level, text string) {
	r.conMu.Lock()
	defer r.conMu.Unlock()
	if r.conTrunc {
		return
	}
	if r.conBytes+len(text) > r.o.ConsoleMax {
		text = text[:max(0, r.o.ConsoleMax-r.conBytes)]
		r.conTrunc = true
	}
	r.conBytes += len(text)
	r.con = append(r.con, ConsoleEntry{Level: level, Text: text})
}

func (r *gojaRT) installPerformance() {
	p := r.vm.NewObject()
	_ = p.Set("now", func() float64 {
		return float64(r.virtual)/1e6 + float64(0)
	})
	_ = r.vm.Set("performance", p)
}

func (r *gojaRT) installTimers() {
	add := func(repeat bool) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			fn, ok := goja.AssertFunction(c.Argument(0))
			if !ok {
				panic(r.vm.NewTypeError("callback must be a function"))
			}
			if r.timerCount >= r.o.MaxTimers {
				panic(r.vm.NewGoError(ErrTimers))
			}
			r.timerCount++
			d := time.Duration(c.Argument(1).ToInteger()) * time.Millisecond
			if d < 0 {
				d = 0
			}
			if repeat && d < time.Millisecond {
				d = time.Millisecond
			}
			r.nextTimer++
			t := &timer{id: r.nextTimer, at: r.virtual + d, seq: r.nextTimer, fn: fn, interval: d, repeat: repeat}
			if len(c.Arguments) > 2 {
				t.args = c.Arguments[2:]
			}
			r.timers = append(r.timers, t)
			return r.vm.ToValue(t.id)
		}
	}
	clr := func(c goja.FunctionCall) goja.Value {
		id := c.Argument(0).ToInteger()
		for i, t := range r.timers {
			if t.id == id {
				r.timers = append(r.timers[:i], r.timers[i+1:]...)
				break
			}
		}
		return goja.Undefined()
	}
	_ = r.vm.Set("setTimeout", add(false))
	_ = r.vm.Set("setInterval", add(true))
	_ = r.vm.Set("clearTimeout", clr)
	_ = r.vm.Set("clearInterval", clr)
	_ = r.vm.Set("setImmediate", func(c goja.FunctionCall) goja.Value {
		args := append([]goja.Value{c.Argument(0), r.vm.ToValue(0)}, c.Arguments[min(1, len(c.Arguments)):]...)
		return add(false)(goja.FunctionCall{Arguments: args})
	})
}

// runTimers fires due timers in virtual-time order until none remain or the
// virtual-time cap is hit. Each callback is a Go->JS call, which makes goja
// drain the promise job queue before returning.
func (r *gojaRT) runTimers() error {
	for len(r.timers) > 0 {
		sort.SliceStable(r.timers, func(i, j int) bool {
			if r.timers[i].at != r.timers[j].at {
				return r.timers[i].at < r.timers[j].at
			}
			return r.timers[i].seq < r.timers[j].seq
		})
		t := r.timers[0]
		if t.at > r.o.VirtualSpan {
			r.timers = nil
			return nil
		}
		r.timers = r.timers[1:]
		if t.at > r.virtual {
			r.virtual = t.at
		}
		if t.repeat {
			if r.timerCount >= r.o.MaxTimers {
				return ErrTimers
			}
			r.timerCount++
			r.nextTimer++
			t.at, t.seq = r.virtual+t.interval, r.nextTimer
			r.timers = append(r.timers, t)
		}
		if _, err := t.fn(goja.Undefined(), t.args...); err != nil {
			return err
		}
	}
	return nil
}

func (r *gojaRT) Eval(ctx context.Context, name, src string) (res Result, err error) {
	if r.closed {
		return res, errors.New("jsrt: runtime closed")
	}
	if len(src) > r.o.MaxSource {
		return res, ErrSourceSize
	}
	// A previous Eval may have been interrupted (timeout/cancel) or have a
	// late interrupt still pending; clear it so the runtime stays usable,
	// e.g. to read back partial results after a limit hit.
	r.vm.ClearInterrupt()
	begin := time.Now()
	var cause error
	var mu sync.Mutex
	interrupt := func(e error) {
		mu.Lock()
		if cause == nil {
			cause = e
		}
		mu.Unlock()
		r.vm.Interrupt(e.Error())
	}
	tm := time.AfterFunc(r.o.Timeout, func() { interrupt(ErrTimeout) })
	stop := context.AfterFunc(ctx, func() { interrupt(ErrCanceled) })
	defer func() {
		tm.Stop()
		stop()
		res.Elapsed = time.Since(begin)
		r.conMu.Lock()
		res.Console, res.ConsoleTruncated = r.con, r.conTrunc
		r.con = nil
		r.conMu.Unlock()
		mu.Lock()
		c := cause
		mu.Unlock()
		if c != nil {
			err = c
		}
	}()

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("jsrt: engine panic: %v", p)
		}
	}()

	prog, cerr := goja.Compile(name, src, false)
	if cerr != nil {
		return res, &ScriptError{Message: cerr.Error()}
	}
	v, rerr := r.vm.RunProgram(prog)
	if rerr == nil {
		rerr = r.runTimers()
	}
	if rerr != nil {
		return res, convertErr(rerr)
	}
	res.Value = r.settle(v)
	// Timers scheduled from promise continuations after settle.
	if terr := r.runTimers(); terr != nil {
		return res, convertErr(terr)
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		res.Value = settled(p)
		if p.State() == goja.PromiseStateRejected {
			return res, &ScriptError{Message: "unhandled promise rejection: " + p.Result().String()}
		}
	}
	return res, nil
}

func (r *gojaRT) settle(v goja.Value) any {
	if v == nil {
		return nil
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		return settled(p)
	}
	return v.Export()
}

func settled(p *goja.Promise) any {
	if p.State() == goja.PromiseStateFulfilled {
		return p.Result().Export()
	}
	return nil
}

func convertErr(e error) error {
	var ie *goja.InterruptedError
	if errors.As(e, &ie) {
		if v, ok := ie.Value().(string); ok {
			switch v {
			case ErrTimeout.Error():
				return ErrTimeout
			case ErrCanceled.Error():
				return ErrCanceled
			}
		}
		return ErrCanceled
	}
	if errors.Is(e, ErrTimers) {
		return ErrTimers
	}
	var ex *goja.Exception
	if errors.As(e, &ex) {
		if ge, ok := ex.Value().Export().(error); ok && errors.Is(ge, ErrTimers) {
			return ErrTimers
		}
		return &ScriptError{Message: ex.Error(), Stack: ex.String()}
	}
	var so *goja.StackOverflowError
	if errors.As(e, &so) {
		return &ScriptError{Message: "stack overflow", Stack: so.String()}
	}
	return e
}
