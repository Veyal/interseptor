package main

import (
	"testing"
	"time"
)

func TestParseUIURL(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "real startup line",
			line: `2026/08/01 00:00:00 Interseptor v1.7.2 · project "default": proxy on 127.0.0.1:8080 · UI on http://127.0.0.1:9966 · data /Users/x/.interseptor`,
			want: "http://127.0.0.1:9966",
		},
		{
			name: "non-default port from a persisted setting",
			line: `Interseptor v1.7.2 · project "p": proxy on 127.0.0.1:8081 · UI on http://127.0.0.1:9971 · data /x`,
			want: "http://127.0.0.1:9971",
		},
		{
			name: "trailing punctuation is not swallowed",
			line: `UI on http://127.0.0.1:9966·`,
			want: "http://127.0.0.1:9966",
		},
		{name: "unrelated line", line: "listening on something", want: ""},
		{name: "empty", line: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseUIURL(tt.line)
			if tt.want == "" {
				if ok {
					t.Errorf("parseUIURL(%q) = %q, want no match", tt.line, got)
				}
				return
			}
			if !ok || got != tt.want {
				t.Errorf("parseUIURL(%q) = %q,%v want %q", tt.line, got, ok, tt.want)
			}
		})
	}
}

func TestURLSnifferReportsFirstMatch(t *testing.T) {
	s := newURLSniffer()
	if _, err := s.Write([]byte("starting up\nInterseptor v1 · UI on http://127.0.0.1:9971 · data /x\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case got := <-s.found:
		if got != "http://127.0.0.1:9971" {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("sniffer did not report the URL")
	}
}

// The child's output arrives in arbitrary chunks, so a URL split across two
// writes must still be recognised.
func TestURLSnifferHandlesSplitWrites(t *testing.T) {
	s := newURLSniffer()
	s.Write([]byte("Interseptor v1 · UI on http://127.0"))
	select {
	case <-s.found:
		t.Fatal("reported before a full line arrived")
	default:
	}
	s.Write([]byte(".0.1:9980 · data /x\n"))
	select {
	case got := <-s.found:
		if got != "http://127.0.0.1:9980" {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("sniffer did not report the URL after the split write")
	}
}

// Writes must keep succeeding (and not block) long after the URL was found, or
// the child would stall on a full pipe.
func TestURLSnifferKeepsAcceptingWrites(t *testing.T) {
	s := newURLSniffer()
	s.Write([]byte("UI on http://127.0.0.1:9966\n"))
	<-s.found
	for i := 0; i < 1000; i++ {
		if _, err := s.Write([]byte("more log output\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
}

// A long stream with no match must not grow the buffer without bound.
func TestURLSnifferBoundsBuffer(t *testing.T) {
	s := newURLSniffer()
	for i := 0; i < 500; i++ {
		s.Write([]byte("a line with no url in it at all and it is fairly long\n"))
	}
	if n := s.buffered(); n > sniffBufferMax {
		t.Errorf("buffer grew to %d bytes, want <= %d", n, sniffBufferMax)
	}
}
