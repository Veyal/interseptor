---
name: child-process-pipes
description: Keep long-running Interseptor child processes live by draining stdout and stderr pipes for the full process lifetime, even after parsing the one value the parent needs.
---

# Child-process pipe lifecycle

Use this when starting a long-running child with `StdoutPipe` or `StderrPipe`.

## Parse without abandoning the pipe

Finding a URL, readiness marker, token, or status line is not a reason to stop reading. OS pipe
buffers are finite; if the child keeps logging after the parent returns from its reader loop, the
child can block once the pipe fills.

Publish the first needed value idempotently, then keep scanning through EOF. If a scanner stops
early because of a token-size or other read error, fall back to `io.Copy(io.Discard, pipe)` so raw
bytes still drain until the child exits.

Tests should use a reader that returns the marker and later log output in separate reads. Assert
that both chunks were consumed; a plain `strings.Reader` may let `bufio.Scanner` prefetch everything
and hide an early-return regression.
