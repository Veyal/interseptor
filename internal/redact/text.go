package redact

import (
	"regexp"
	"strings"
)

// Text masks credentials in free text before it is drawn or stored: credential
// header lines, Bearer/Basic schemes, JWTs, key=value and JSON secrets under
// snake_case, kebab-case and camelCase key names (accessToken, csrf_token,
// otp, reset_code, ?code=123456) and long opaque tokens. Each masked value
// becomes a length+digest placeholder so two equal secrets stay comparable
// without being shown. Placeholders are left alone, so Text is idempotent.
//
// Short standalone values with no recognisable key cannot be detected; callers
// that draw such values (for example extracted response fields) must mask them
// themselves.
func Text(s string) string {
	if s == "" {
		return s
	}
	s = headerLine.ReplaceAllStringFunc(s, sub(headerLine, 1, 2, 3))
	s = authScheme.ReplaceAllStringFunc(s, sub(authScheme, 1, 2, 3))
	s = jwtAny.ReplaceAllStringFunc(s, mask)
	s = keyedLong.ReplaceAllStringFunc(s, sub(keyedLong, 1, 2, 3))
	s = keyedShort.ReplaceAllStringFunc(s, sub(keyedShort, 1, 2, 3))
	s = numericCode.ReplaceAllStringFunc(s, sub(numericCode, 1, 2, 3))
	return longOpaque.ReplaceAllStringFunc(s, func(m string) string {
		if opaque(m) {
			return mask(m)
		}
		return m
	})
}

var (
	headerLine = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|set-cookie|cookie|x-api-key|x-auth-token|x-access-token|x-csrf-token|x-xsrf-token)\b(\s*[:=]\s*)([^\r\n]+)`)
	authScheme = regexp.MustCompile(`(?i)\b(bearer|basic|digest|negotiate)(\s+)([A-Za-z0-9._~+/=:-]{6,})`)
	jwtAny     = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	// keyedLong matches any key name that merely contains a secret word, so
	// accessToken, csrf_token and x-xsrf-token all qualify.
	keyedLong = regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*(?:token|secret|passw(?:or)?d|pwd|passcode|api[_-]?key|access[_-]?key|private[_-]?key|csrf|xsrf|signature|credential)[A-Za-z0-9_.\-]*)(["']?\s*[:=]\s*["']?)([^&\s"',;}\]]+)`)
	// keyedShort needs whole-word keys so "pinned" or "consider" never match.
	keyedShort  = regexp.MustCompile(`(?i)(\b(?:sid|pin|otp|session(?:[_-]?id)?|(?:reset|verification|verify|auth|confirm[a-z]*|login|mfa|sms|email)[_-]?code)\b)(["']?\s*[:=]\s*["']?)([^&\s"',;}\]]+)`)
	numericCode = regexp.MustCompile(`(?i)(\bcode\b)(["']?\s*[:=]\s*["']?)(\d{4,8})\b`)
	longOpaque  = regexp.MustCompile(`[A-Za-z0-9_+=]{32,}`)
)

func mask(v string) string { return Describe(v).Placeholder() }

// sub builds a ReplaceAllStringFunc callback that keeps the key and separator
// groups and masks the value group.
func sub(re *regexp.Regexp, keyGrp, sepGrp, valGrp int) func(string) string {
	return func(m string) string {
		p := re.FindStringSubmatch(m)
		if len(p) <= valGrp || strings.HasPrefix(p[valGrp], "[redacted") {
			return m
		}
		return p[keyGrp] + p[sepGrp] + mask(p[valGrp])
	}
}

// opaque is true for long runs mixing letters and digits (API keys, session
// ids, hashes); long plain words and identifiers are left alone.
func opaque(s string) bool {
	letter, digit := false, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return letter && digit
}
