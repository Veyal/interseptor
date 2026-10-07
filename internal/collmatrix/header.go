package collmatrix

import (
	"strings"
)

// Header is one request or response header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParseHeaderLines reads "Key: Value" lines (the authz identity format).
// Blank lines and lines without a colon are ignored.
func ParseHeaderLines(s string) []Header {
	var out []Header
	for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		i := strings.IndexByte(ln, ':')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(ln[:i])
		if name == "" || strings.ContainsAny(name, " \t") {
			continue
		}
		out = append(out, Header{Name: name, Value: strings.TrimSpace(ln[i+1:])})
	}
	return out
}

// credentialHeaders are always removed when sending as another identity, so a
// credential from the collection can never leak into another identity's run.
var credentialHeaders = []string{"authorization", "cookie", "proxy-authorization"}

func hasHeaderName(set []string, name string) bool {
	for _, s := range set {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}
