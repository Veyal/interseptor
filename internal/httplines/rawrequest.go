package httplines

import (
	"bufio"
	"net/http"
	"strings"
)

// NormalizeCRLF converts LF-only newlines to CRLF (Burp-style raw editor).
func NormalizeCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// ParseRawRequest parses a raw HTTP request (request line + headers + body after a
// blank line). Content-Length is dropped — callers recompute it from the body.
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
	if req.Host != "" {
		h.Set("Host", req.Host)
	}
	p := req.URL.RequestURI()
	if p == "" {
		p = "/"
	}
	return req.Method, p, h, []byte(bodyStr), nil
}
