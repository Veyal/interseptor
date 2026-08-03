package main

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
)

// sniffBufferMax bounds how much unmatched output the sniffer retains. Only the
// tail matters — a URL split across writes spans a few dozen bytes at most.
const sniffBufferMax = 8 << 10

// uiURLRe matches the control URL the server logs at startup, e.g.
//
//	Interseptor v1.7.2 · project "p": proxy on 127.0.0.1:8080 · UI on http://127.0.0.1:9966 · data /x
//
// The character class stops at whitespace and at the "·" separator the log line
// uses, so the trailing field is not swallowed into the URL.
var uiURLRe = regexp.MustCompile(`UI on (https?://[^\s·]+)`)

// parseUIURL extracts the control URL from a startup log line.
func parseUIURL(line string) (string, bool) {
	m := uiURLRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimRight(m[1], ".,;"), true
}

// urlSniffer is an io.Writer that watches the server's output for the control
// URL it logs at startup and publishes the first match on found.
//
// The launcher needs this because the control address resolves CLI → env →
// PERSISTED SETTING → default (see resolveControlAddr in cmd/interseptor). A
// stored control.addr means the child can bind an address the launcher would
// never guess, so the authoritative answer is whatever the child reports.
type urlSniffer struct {
	found chan string

	mu   sync.Mutex
	buf  []byte
	done bool
}

func newURLSniffer() *urlSniffer {
	return &urlSniffer{found: make(chan string, 1)}
}

// Write never blocks and never fails: it sits in the child's stdout path, so
// stalling here would stall the server.
func (s *urlSniffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return len(p), nil
	}
	s.buf = append(s.buf, p...)
	// Only parse COMPLETE lines. Matching a partial buffer would happily accept a
	// half-arrived URL ("http://127.0") as if it were the whole thing.
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := string(s.buf[:i])
		s.buf = s.buf[i+1:]
		if u, ok := parseUIURL(line); ok {
			s.done = true
			s.buf = nil
			select {
			case s.found <- u:
			default:
			}
			return len(p), nil
		}
	}
	// Only a partial trailing line is retained, but cap it so a stream with no
	// newlines at all cannot grow without bound.
	if len(s.buf) > sniffBufferMax {
		s.buf = s.buf[len(s.buf)-sniffBufferMax:]
	}
	return len(p), nil
}

// buffered reports the retained byte count (test hook for the bound).
func (s *urlSniffer) buffered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}
