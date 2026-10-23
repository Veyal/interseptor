package redact

import (
	"strings"
)

// CredentialHeaderNames are the HTTP header names whose value is a credential.
// One list drives both the free-text pass (Text) and the raw-message pass
// (CredentialHeaders) so the two can never disagree about which headers count.
// Order is longest-prefix-first so an alternation built from it matches
// "Proxy-Authorization" and "Set-Cookie" whole rather than their suffixes.
var CredentialHeaderNames = []string{
	"proxy-authorization", "authorization", "set-cookie", "cookie",
	"x-api-key", "x-auth-token", "x-access-token", "x-csrf-token", "x-xsrf-token",
}

// credentialHeaderSegments are whole name segments (split on '-' and '_') that
// mark any other header as carrying a credential (X-Amz-Security-Token,
// PRIVATE-TOKEN, X-Vault-Token, X-Amz-Credential, X-Hub-Signature-256, X-Auth).
// Whole segments keep X-Author or X-Session-Count clear.
var credentialHeaderSegments = map[string]bool{
	"token": true, "secret": true, "password": true, "passwd": true, "credential": true,
	"credentials": true, "signature": true, "auth": true, "apikey": true, "accesskey": true,
	"sessionid": true, "sessiontoken": true,
}

// credentialHeaderPairs are adjacent segment pairs that name a credential
// (Api-Key, X-Access-Key, X-Session-Id).
var credentialHeaderPairs = map[string]bool{
	"api key": true, "access key": true, "session id": true, "session key": true, "private key": true,
}

// isEvidenceHeader names headers that mention credentials but carry protocol
// evidence a finding may rest on: authentication challenges and CORS policy.
func isEvidenceHeader(name string) bool {
	switch name {
	case "www-authenticate", "proxy-authenticate", "authentication-info", "proxy-authentication-info":
		return true
	}
	return strings.HasPrefix(name, "access-control-")
}

func hasCredentialSegment(name string) bool {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if credentialHeaderSegments[p] {
			return true
		}
		if i > 0 && credentialHeaderPairs[parts[i-1]+" "+p] {
			return true
		}
	}
	return false
}

var credentialHeaderSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(CredentialHeaderNames))
	for _, n := range CredentialHeaderNames {
		m[n] = struct{}{}
	}
	return m
}()

// CredentialHeaders masks the value of every CredentialHeaderNames header, and
// of every header whose name carries a credential segment (hasCredentialSegment),
// in a raw HTTP message, plus secret-named query values on the request line,
// leaving every other header and the body exactly as captured. Each masked value becomes the same
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
			out.WriteString(maskQueryValues(line))
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
//
// A bare "\r" also ends a line: the report and preview renderers split on it,
// so treating it as line content would let "X-Foo: a\rAuthorization: ..." draw
// an unmasked credential on its own line.
func cutLine(s string) (line, term, rest string) {
	i := strings.IndexAny(s, "\r\n")
	if i < 0 {
		return s, "", ""
	}
	if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
		return s[:i], "\r\n", s[i+2:]
	}
	return s[:i], s[i : i+1], s[i+1:]
}

// maskQueryValues masks secret-named query parameters on a request line
// (?access_token=...&api_key=...) with the same key=value rules Text uses.
func maskQueryValues(line string) string {
	line = keyedLong.ReplaceAllStringFunc(line, sub(keyedLong, 1, 2, 3))
	line = keyedShort.ReplaceAllStringFunc(line, sub(keyedShort, 1, 2, 3))
	return numericCode.ReplaceAllStringFunc(line, sub(numericCode, 1, 2, 3))
}

func isCredentialHeaderName(name string) bool {
	name = strings.ToLower(name)
	if _, ok := credentialHeaderSet[name]; ok {
		return true
	}
	return !isEvidenceHeader(name) && hasCredentialSegment(name)
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
	if !isCredentialHeaderName(name) {
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
