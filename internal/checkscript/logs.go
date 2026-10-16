package checkscript

import (
	"fmt"
	"sync"

	"go.starlark.net/starlark"
)

// Starlark's default print() writes to the process's stderr, which is nowhere
// useful: the author is in the check editor or calling POST /api/checks/test,
// and the output lands in the server log at best. Capturing it is the
// difference between "my check returns nothing" and seeing why.

const (
	// maxLogLines bounds how many print() lines one run keeps. A check that
	// prints inside a loop over a large body can produce thousands.
	maxLogLines = 200
	// maxLogBytes bounds the total captured output, so one `print("x" * 1e6)`
	// cannot balloon a scan's memory.
	maxLogBytes = 32 << 10
)

// printLog collects a check's print() output under both caps. The mutex is
// there because a check run and its compile each build their own log, but a
// Starlark thread can in principle call Print from a nested call — the lock
// keeps the slice consistent whatever the call shape.
type printLog struct {
	mu        sync.Mutex
	lines     []string
	bytes     int
	truncated bool
}

// add is the starlark.Thread.Print hook.
func (l *printLog) add(_ *starlark.Thread, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.truncated {
		return
	}
	if len(l.lines) >= maxLogLines {
		l.cut(fmt.Sprintf("… print output truncated: more than %d lines", maxLogLines))
		return
	}
	if l.bytes+len(msg) > maxLogBytes {
		if room := maxLogBytes - l.bytes; room > 0 {
			l.lines = append(l.lines, msg[:room])
			l.bytes += room
		}
		l.cut(fmt.Sprintf("… print output truncated: over the %d-byte cap", maxLogBytes))
		return
	}
	l.lines = append(l.lines, msg)
	l.bytes += len(msg)
}

// cut appends the truncation notice and stops further capture. Caller holds mu.
func (l *printLog) cut(notice string) {
	l.truncated = true
	l.lines = append(l.lines, notice)
}

// out returns the captured lines. A check that printed nothing returns nil, so
// a caller can tell "no output" from "output was empty strings".
func (l *printLog) out() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
