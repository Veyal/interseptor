package scriptworker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
)

// The wall-clock deadline must bound the whole run, including handing the
// request to the worker. A worker that never reads stdin (a stopped process, a
// crash before Serve, a non-worker binary) used to block the supervisor inside
// the run-frame write — before the deadline timer was even armed — so a script
// larger than the pipe buffer hung the caller indefinitely.
func TestRunFrameWriteHonorsWallClockDeadline(t *testing.T) {
	in := input(strings.Repeat("// padding\n", 100000)) // ~1.1 MiB: larger than a pipe buffer
	in.Limits.Timeout = 200 * time.Millisecond
	w := Subprocess{Path: "/bin/sleep", Args: []string{"20"}, Grace: 200 * time.Millisecond}

	done := make(chan pmsandbox.Output, 1)
	begin := time.Now()
	go func() { done <- w.Run(context.Background(), in) }()
	select {
	case o := <-done:
		if k := errKind(o); k != KindTimeout {
			t.Fatalf("kind=%q, want %q; errors=%+v", k, KindTimeout, o.Errors)
		}
		if d := time.Since(begin); d > 5*time.Second {
			t.Fatalf("run returned after %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Subprocess.Run blocked for %v: the deadline was not enforced while writing the run frame", time.Since(begin))
	}
}
