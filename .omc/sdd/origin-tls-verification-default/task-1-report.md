# Task 1 — Origin TLS runtime policy

## Status
Implemented origin-only TLS verification policy in `internal/proxy`.

## Changes
- Added atomic `Server.originTLSVerify`, defaulting to `false`.
- Added `SetOriginTLSVerify(bool)` and `OriginTLSVerify() bool`.
- Origin TLS config now always overrides cloned transport `InsecureSkipVerify` using policy plus matching origin exception precedence.
- Existing cloned `RootCAs` and `NextProtos` remain preserved.
- Policy changes close idle transport connections; active connections are not forcibly closed.
- HTTPS upstream proxy TLS remains strict and continues using configured upstream roots.
- Added focused default, strict, exception, normalization, and transport-config tests.

## Verification
- `go test ./internal/proxy -run 'TestOriginTLSVerify' -count=1`
- `go test -race ./internal/proxy -run 'TestOriginTLSVerify' -count=1`

Both passed.

## Scope
No control, UI, release, formatter, linter, or project-wide suite changes included.
