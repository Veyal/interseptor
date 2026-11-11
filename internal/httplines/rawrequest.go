package httplines

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// NormalizeCRLF converts LF-only newlines to CRLF (Burp-style raw editor).
func NormalizeCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// ParseRawRequest parses a raw HTTP request (request line + headers + body after a
// blank line). Content-Length is dropped — callers recompute it from the body.
// Transfer-Encoding is kept: the body is then the framed bytes as written.
func ParseRawRequest(raw string) (method, path string, headers map[string][]string, body []byte, err error) {
	norm := NormalizeCRLF(raw)
	head := norm
	var bodyStr string
	if i := strings.Index(norm, "\r\n\r\n"); i >= 0 {
		head = norm[:i] + "\r\n\r\n"
		bodyStr = norm[i+4:]
	} else {
		head = strings.TrimRight(norm, "\r\n") + "\r\n\r\n"
	}

	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(head)))
	if err != nil {
		return "", "", nil, nil, err
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Del("Content-Length")
	// http.ReadRequest moves Transfer-Encoding out of Header; put it back so a
	// chunked template keeps its framing instead of being re-framed by length.
	if len(req.TransferEncoding) > 0 {
		h["Transfer-Encoding"] = append([]string(nil), req.TransferEncoding...)
	}
	if req.Host != "" {
		h.Set("Host", req.Host)
	}
	p := req.URL.RequestURI()
	if p == "" {
		p = "/"
	}
	// The second result is a path that callers append to scheme://host. A
	// request target that is neither origin-form (/path), absolute-form
	// (http://host/path, reduced to its path above) nor asterisk-form (*) has
	// no path: "GET x:@other.example/a" is an opaque URI whose RequestURI is
	// "@other.example/a", and appended to a target that moves the authority to
	// other.example. Refuse it here so no caller ever sees it as a path.
	if req.URL.Opaque != "" || (p != "*" && !strings.HasPrefix(p, "/")) {
		return "", "", nil, nil, fmt.Errorf("%w: %q (use /path, http://host/path or *)", ErrRequestTarget, clip(req.RequestURI, 80))
	}
	return req.Method, p, h, []byte(bodyStr), nil
}

// ErrRequestTarget is wrapped by ParseRawRequest when the request line's target
// is not something a path can be taken from.
var ErrRequestTarget = errors.New("request target is not a path")

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
