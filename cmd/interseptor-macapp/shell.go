package main

import (
	"flag"
	"io"
	"strings"
	"sync"
)

// Shell mode is the contract between this supervisor and the native
// Interseptor.app shell (packaging/macos/shell). The shell owns the window; this
// process owns the server. Stdout carries only protocol lines:
//
//	INTERSEPTOR_READY <control url>   once the control UI accepts connections
//	INTERSEPTOR_ERROR <message>       when startup (or supervision) fails
//
// Stdin is the parent's lifeline: when it reaches EOF the shell has gone away,
// and a server this process started is stopped gracefully.
const (
	readyPrefix = "INTERSEPTOR_READY "
	errorPrefix = "INTERSEPTOR_ERROR "
)

type options struct {
	shell bool
}

// parseOptions reads the launcher's command line. Finder historically passed a
// -psn_<serial> argument to launched apps; it is dropped rather than rejected.
func parseOptions(args []string) (options, error) {
	var kept []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			kept = append(kept, a)
		}
	}
	fs := flag.NewFlagSet("interseptor-macapp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o options
	fs.BoolVar(&o.shell, "shell", false, "report readiness on stdout for the native app shell instead of opening a window")
	if err := fs.Parse(kept); err != nil {
		return options{}, err
	}
	return o, nil
}

func readyLine(url string) string { return readyPrefix + url + "\n" }

// errorLine folds msg onto one line: the shell parses one message per line.
func errorLine(msg string) string {
	return errorPrefix + strings.Join(strings.Fields(msg), " ") + "\n"
}

// watchEOF returns a channel closed once r reaches EOF (or fails), draining and
// discarding anything written to it.
func watchEOF(r io.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	return done
}

// protocolWriter serialises protocol lines onto the shell's stdout pipe.
type protocolWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newProtocolWriter(w io.Writer) *protocolWriter { return &protocolWriter{w: w} }

func (p *protocolWriter) ready(url string) { p.write(readyLine(url)) }

func (p *protocolWriter) fail(msg string) { p.write(errorLine(msg)) }

func (p *protocolWriter) write(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = io.WriteString(p.w, line)
}
