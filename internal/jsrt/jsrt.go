// Package jsrt is the engine-neutral JavaScript runtime used for collection
// scripts. Callers depend only on the Runtime interface; the concrete engine
// (goja, see goja.go) is an implementation detail so it can be swapped.
//
// Scripts are untrusted code. The runtime exposes no ambient I/O: there is no
// filesystem, process, environment, network, fetch or require. Time and
// randomness are injected (Options.Clock, Options.Rand) so runs are
// reproducible. Everything the script can reach is passed in explicitly via
// Globals.
package jsrt

import (
	"context"
	"errors"
	"time"
)

// Default limits. All are overridable through Options; zero means default.
const (
	DefaultTimeout     = 5 * time.Second
	MaxTimeout         = 60 * time.Second
	DefaultMaxSource   = 512 << 10
	DefaultConsoleMax  = 256 << 10
	DefaultStackDepth  = 256
	DefaultMaxTimers   = 1000
	DefaultVirtualSpan = 30 * time.Second
)

// Sentinel errors, matchable with errors.Is.
var (
	ErrTimeout    = errors.New("jsrt: script timed out")
	ErrCanceled   = errors.New("jsrt: script canceled")
	ErrSourceSize = errors.New("jsrt: source too large")
	ErrTimers     = errors.New("jsrt: too many timers")
)

// Options configure a Runtime. The zero value is usable.
type Options struct {
	// Timeout is the wall-clock interrupt deadline per Eval (default 5s,
	// capped at MaxTimeout). It covers synchronous code, promise jobs and
	// timer callbacks together.
	Timeout time.Duration
	// Clock supplies "now" for Date and performance.now. nil = time.Now.
	// Timers advance a virtual offset on top of it; they never sleep.
	Clock func() time.Time
	// Rand supplies Math.random values in [0,1). nil = non-deterministic.
	Rand func() float64
	// MaxSource caps script source bytes; ConsoleMax caps captured console
	// bytes; StackDepth caps call depth; MaxTimers caps live+fired timers;
	// VirtualSpan caps total virtual time timers may advance.
	MaxSource   int
	ConsoleMax  int
	StackDepth  int
	MaxTimers   int
	VirtualSpan time.Duration
}

// ConsoleEntry is one captured console call.
type ConsoleEntry struct {
	Level string `json:"level"` // log, info, warn, error, debug
	Text  string `json:"text"`
}

// Result is the outcome of one Eval.
type Result struct {
	// Value is the script's completion value exported to plain Go types
	// (nil, bool, int64, float64, string, []any, map[string]any). If the
	// completion value is a Promise it is the settled value.
	Value any
	// Console holds captured console output, truncated at ConsoleMax.
	Console []ConsoleEntry
	// ConsoleTruncated reports that output beyond ConsoleMax was dropped.
	ConsoleTruncated bool
	// Elapsed is real wall time spent in Eval.
	Elapsed time.Duration
}

// Runtime evaluates scripts. A Runtime is single-use-per-phase by convention
// (fresh runtime per script phase) and is NOT safe for concurrent use.
type Runtime interface {
	// Set exposes a Go value to scripts as a global. Supported: nil, bool,
	// ints, floats, string, []any, map[string]any, and Func.
	Set(name string, v any) error
	// Eval runs src. Errors from script exceptions are *ScriptError;
	// limit hits wrap ErrTimeout/ErrCanceled/ErrSourceSize/ErrTimers.
	Eval(ctx context.Context, name, src string) (Result, error)
	// Close releases the runtime. Further use is an error.
	Close()
}

// Func is a Go function callable from scripts. args are exported JS values.
// Returned error is thrown into the script as an Error.
type Func func(args []any) (any, error)

// ScriptError is an uncaught script exception.
type ScriptError struct {
	Message string
	Stack   string
}

func (e *ScriptError) Error() string { return e.Message }

// New returns the default engine (goja).
func New(o Options) Runtime { return newGoja(o) }
