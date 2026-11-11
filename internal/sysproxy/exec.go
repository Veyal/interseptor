package sysproxy

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// commandTimeout bounds every external helper (networksetup, gsettings). They
// normally answer in milliseconds. A var so tests can shrink it.
var commandTimeout = 15 * time.Second

// runCommand runs name with args and returns its combined output, giving up
// after commandTimeout: these helpers run inside the settings handler, at
// startup recovery and on shutdown, where a child that never returns would
// otherwise hang the caller for good.
func runCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// A killed helper can leave a descendant holding the output pipe; do not
	// wait on that pipe indefinitely either.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, commandTimeout)
	}
	return out, err
}
