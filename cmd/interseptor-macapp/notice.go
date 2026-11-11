package main

import (
	"bytes"
	"regexp"
	"sync"
)

// resumeFallbackEnv asks the server to open the default project when the
// external folder it remembers is unavailable (an unplugged drive), instead of
// refusing to start: a double-clicked app passes no flags and has no terminal,
// so that refusal would repeat on every launch. The server leaves the folder
// remembered, so reconnecting it and relaunching resumes it. See
// cmd/interseptor/resume_fallback.go.
const resumeFallbackEnv = "INTERSEPTOR_RESUME_FALLBACK=default"

// startupNoticeRe matches the server's startup notice: a whole log line whose
// message starts with the marker, never a marker embedded in other output.
var startupNoticeRe = regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d(?:\.\d+)? startup notice: (.+)$`)

// maxStartupNotices bounds how many notices one start can queue.
const maxStartupNotices = 4

// noticeSniffer is an io.Writer that collects the startup notices the server
// logs before it reports its control URL. It stops looking at that line: once
// the proxy is serving, log lines can carry text chosen by a remote party, and
// that must never be shown to the operator as an application message.
type noticeSniffer struct {
	mu      sync.Mutex
	buf     []byte
	done    bool
	pending []string
}

// Write never blocks and never fails: it sits in the child's output path.
func (s *noticeSniffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return len(p), nil
	}
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(s.buf[:i], "\r"))
		s.buf = s.buf[i+1:]
		if m := startupNoticeRe.FindStringSubmatch(line); m != nil && len(s.pending) < maxStartupNotices {
			s.pending = append(s.pending, m[1])
		}
		if _, ok := parseUIURL(line); ok {
			s.done, s.buf = true, nil
			return len(p), nil
		}
	}
	if len(s.buf) > sniffBufferMax {
		s.buf = s.buf[len(s.buf)-sniffBufferMax:]
	}
	return len(p), nil
}

// take returns the notices collected so far, each exactly once.
func (s *noticeSniffer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	return out
}

// buffered reports the retained byte count (test hook for the bound).
func (s *noticeSniffer) buffered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}
