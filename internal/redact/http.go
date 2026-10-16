package redact

import "strings"

// CredentialHeaderNames are the HTTP header names whose value is a credential.
// One list drives both the free-text pass (Text) and the raw-message pass
// (CredentialHeaders) so the two can never disagree about which headers count.
// Order is longest-prefix-first so an alternation built from it matches
// "Proxy-Authorization" and "Set-Cookie" whole rather than their suffixes.
var CredentialHeaderNames = []string{
	"proxy-authorization", "authorization", "set-cookie", "cookie",
	"x-api-key", "x-auth-token", "x-access-token", "x-csrf-token", "x-xsrf-token",
}

var credentialHeaderSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(CredentialHeaderNames))
	for _, n := range CredentialHeaderNames {
		m[n] = struct{}{}
	}
	return m
}()

// CredentialHeaders masks the value of every CredentialHeaderNames header in a
// raw HTTP message, leaving the request/status line, every other header, and
// the body exactly as captured. Each masked value becomes the same
// length+digest placeholder Text uses, so a report reader can still tell two
// requests apart by whether they carried the same credential.
//
// The scope is deliberately the header block only. A raw request/response is
// attached to a finding as evidence: rewriting its body would damage the proof
// the finding rests on, and a credential that appears in a body is reported by
// Scan instead. Both the header block and the body stop at the first empty
// line, as on the wire; CR/LF or bare LF endings are preserved byte for byte.
//
// Masking is idempotent: a value that is already a placeholder is left alone.
func CredentialHeaders(raw string) string {
	if raw == "" {
		return raw
	}
	var out strings.Builder
	out.Grow(len(raw))
	rest, first, folding := raw, true, false
	for rest != "" {
		line, term, more := cutLine(rest)
		rest = more
		if line == "" && !first {
			// The empty line ends the header block; the body follows verbatim.
			out.WriteString(term)
			out.WriteString(rest)
			return out.String()
		}
		switch {
		case first:
			first = false
			out.WriteString(line)
		case isHeaderContinuation(line):
			if folding {
				out.WriteString(maskHeaderContinuation(line))
			} else {
				out.WriteString(line)
			}
		default:
			masked, isCredential := maskCredentialHeaderLine(line)
			folding = isCredential
			out.WriteString(masked)
		}
		out.WriteString(term)
	}
	return out.String()
}

// cutLine splits off the first line of s, returning it without its terminator,
// the terminator itself ("\r\n", "\n" or "" at end of input), and the remainder.
func cutLine(s string) (line, term, rest string) {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s, "", ""
	}
	if i > 0 && s[i-1] == '\r' {
		return s[:i-1], "\r\n", s[i+1:]
	}
	return s[:i], "\n", s[i+1:]
}

func isHeaderContinuation(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

// maskCredentialHeaderLine masks one "Name: value" line and reports whether the
// name is a credential header (true even when there was nothing left to mask,
// so an obsolete folded continuation of that header is masked too).
func maskCredentialHeaderLine(line string) (string, bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return line, false
	}
	name := strings.ToLower(strings.TrimSpace(line[:colon]))
	if _, ok := credentialHeaderSet[name]; !ok {
		return line, false
	}
	value := line[colon+1:]
	ws := value[:len(value)-len(strings.TrimLeft(value, " \t"))]
	value = value[len(ws):]
	if value == "" || strings.HasPrefix(value, "[redacted") {
		return line, true
	}
	return line[:colon+1] + ws + mask(value), true
}

func maskHeaderContinuation(line string) string {
	ws := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	value := line[len(ws):]
	if value == "" || strings.HasPrefix(value, "[redacted") {
		return line
	}
	return ws + mask(value)
}
