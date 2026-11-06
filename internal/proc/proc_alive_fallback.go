//go:build !windows && !linux && !darwin

package proc

// aliveInterseptor falls back to the generic liveness check on platforms
// without a cheap, per-PID image-name lookup wired up (only windows, linux,
// and darwin have one — see proc_windows.go, proc_linux.go, proc_darwin.go).
// These platforms are not part of the release matrix (.goreleaser.yaml only
// builds linux/darwin/windows), so this is reached in practice only by
// `go build`/`go test` on an unlisted GOOS, not by a shipped binary.
func aliveInterseptor(pid int) bool {
	return Alive(pid)
}
