package main

import "io"

// exitOnStdinEOFEnv makes the server treat stdin EOF as a graceful shutdown
// request. A supervisor (interseptor-macapp) sets it and holds the write end of
// the server's stdin pipe for its whole life: if the supervisor is SIGKILLed,
// the kernel closes that pipe and the server shuts down instead of lingering
// as an orphan that the next app launch would adopt and never stop.
const exitOnStdinEOFEnv = "INTERSEPTOR_EXIT_ON_STDIN_EOF"

// stdinLifeline returns a channel closed once r reaches EOF (or fails) when
// exitOnStdinEOFEnv is "1". Otherwise it returns nil, which never fires in a
// select, so an interactive or detached server keeps its old behavior.
func stdinLifeline(getenv func(string) string, r io.Reader) <-chan struct{} {
	if getenv(exitOnStdinEOFEnv) != "1" {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	return done
}
