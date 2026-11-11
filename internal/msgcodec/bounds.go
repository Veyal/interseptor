package msgcodec

import (
	"fmt"
	"runtime"
	"runtime/metrics"
	"time"

	"go.starlark.net/starlark"
)

// The step budget (MaxSteps) bounds interpreter steps, which is neither time
// nor memory: one step can be a builtin hashing a megabyte, and `s = s + s`
// doubles a string in a handful of steps (thirty iterations is a gigabyte, and
// Starlark's own size check covers `*`, not `+`). Every compile, match, decode,
// encode and sign call is therefore also bounded by wall clock, by how far the
// process heap may grow while it runs, and by the size of what it returns.
const (
	// CallTimeout is the wall-clock limit of one codec call.
	CallTimeout = 10 * time.Second
	// MaxCallMemory is how much the process heap may grow during one codec
	// call before the call is cancelled. Starlark does not account memory per
	// thread, so this is measured process-wide and confirmed after a forced
	// collection: garbage the script produced does not count, live growth does.
	MaxCallMemory = 256 << 20
	// MaxResultBytes caps the text a decode, encode or sign call may return.
	MaxResultBytes = 32 << 20
)

// Variables so tests can lower them.
var (
	callTimeout           = CallTimeout
	callMemory     uint64 = MaxCallMemory
	maxResultBytes        = MaxResultBytes
	heapPollEvery         = 5 * time.Millisecond
)

// guard bounds a Starlark thread for the duration of one call by cancelling it
// (as internal/searchscript does for a request context). Cancellation is seen
// between interpreter steps, so a builtin already running finishes first. The
// returned func must be called when the call returns.
func guard(thread *starlark.Thread) (release func()) {
	deadline := time.Now().Add(callTimeout)
	base := heapObjectBytes()
	done := make(chan struct{})
	// Nearly every call returns in microseconds: the watcher only starts for
	// one still running after the first poll interval.
	poll := time.AfterFunc(min(heapPollEvery, callTimeout), func() { watch(thread, deadline, base, done) })
	return func() {
		poll.Stop()
		close(done)
	}
}

// watch cancels thread when the deadline passes or the heap has grown more
// than the memory limit over base, polling until done is closed.
func watch(thread *starlark.Thread, deadline time.Time, base uint64, done <-chan struct{}) {
	timeout, limit := callTimeout, callMemory
	tick := time.NewTicker(heapPollEvery)
	defer tick.Stop()
	over := func() bool {
		cur := heapObjectBytes()
		return cur > base && cur-base > limit
	}
	for {
		if !time.Now().Before(deadline) {
			thread.Cancel(fmt.Sprintf("codec time limit of %s exceeded", timeout))
			return
		}
		if over() {
			runtime.GC() // discount garbage: only growth that is still live counts
			if over() {
				thread.Cancel(fmt.Sprintf("codec memory limit exceeded (the heap grew more than %d MiB during the call)", limit>>20))
				return
			}
		}
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

// heapObjectBytes is the bytes of heap objects, live or not yet swept. Reading
// it does not stop the world.
func heapObjectBytes() uint64 {
	sample := [1]metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample[:])
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

// resultTooLarge reports a result over MaxResultBytes.
func resultTooLarge(what string, n int) error {
	if n <= maxResultBytes {
		return nil
	}
	return fmt.Errorf("%s result exceeds the %d byte limit (%d bytes)", what, maxResultBytes, n)
}

// valueTextSize is the text a decode result would copy out of v: a string, or
// a dict's plaintext, note and fields.
func valueTextSize(v starlark.Value) int {
	if s, ok := starlark.AsString(v); ok {
		return len(s)
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return 0
	}
	n := 0
	for _, key := range []string{"plaintext", "note"} {
		if pv, ok, _ := d.Get(starlark.String(key)); ok {
			if s, ok := starlark.AsString(pv); ok {
				n += len(s)
			}
		}
	}
	if fv, ok, _ := d.Get(starlark.String("fields")); ok {
		if fd, ok := fv.(*starlark.Dict); ok {
			for _, item := range fd.Items() {
				ks, _ := starlark.AsString(item[0])
				vs, _ := starlark.AsString(item[1])
				n += len(ks) + len(vs)
			}
		}
	}
	return n
}
