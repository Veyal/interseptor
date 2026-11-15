package store

import (
	"bytes"
	"regexp"
	"sync"
)

// soft404Re matches common "page not found" phrases in HTML/JSON error pages
// that return HTTP 2xx/3xx instead of an honest 404.
// Retained as the rare-Unicode fallback for inputs carrying U+017F ('ſ') or
// U+212A ('K'), which RE2 folds to ASCII under (?i).
var soft404Re = regexp.MustCompile(`(?i)(` +
	`not\s+found|` +
	`page\s+does\s+not\s+exist|` +
	`doesn'?t\s+exist|` +
	`no\s+such|` +
	`couldn'?t\s+find|` +
	`could\s+not\s+find|` +
	`\b404\b|` +
	`no\s+results|` +
	`nothing\s+found|` +
	`resource\s+not\s+found|` +
	`invalid\s+url|` +
	`unknown\s+page` +
	`)`)

var (
	longSUTF8  = []byte("\xc5\xbf")     // U+017F
	kelvinUTF8 = []byte("\xe2\x84\xaa") // U+212A

	tok404         = []byte("404")
	tokFound       = []byte("found")
	tokExist       = []byte("exist")
	tokFind        = []byte("find")
	tokSuch        = []byte("such")
	tokResults     = []byte("results")
	tokURL         = []byte("url")
	tokPage        = []byte("page")
	tokNot         = []byte("not")
	tokNothing     = []byte("nothing")
	tokDoes        = []byte("does")
	tokDoesnt      = []byte("doesnt")
	tokDoesntQuote = []byte("doesn't")
	tokCould       = []byte("could")
	tokCouldnt     = []byte("couldnt")
	tokCouldntQ    = []byte("couldn't")
	tokNo          = []byte("no")
	tokInvalid     = []byte("invalid")
	tokUnknown     = []byte("unknown")

	soft404BufPool = sync.Pool{
		New: func() any {
			b := make([]byte, maxEndpointBodyReadBytes)
			return &b
		},
	}
)

func isSoft404Content(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if hasSpecialRE2CaseFold(body) {
		return soft404Re.Match(body)
	}
	if len(body) <= 4096 {
		var stack [4096]byte
		lower := stack[:len(body)]
		copyFoldASCII(lower, body)
		return matchSoft404Lower(lower)
	}
	bp := soft404BufPool.Get().(*[]byte)
	buf := *bp
	if cap(buf) < len(body) {
		buf = make([]byte, len(body))
	} else {
		buf = buf[:len(body)]
	}
	copyFoldASCII(buf, body)
	hit := matchSoft404Lower(buf)
	if cap(buf) <= maxEndpointBodyReadBytes {
		*bp = buf
		soft404BufPool.Put(bp)
	}
	return hit
}

// isSoft404Mutable checks body while lower-casing ASCII in place when safe.
// Callers passing a private scratch slice avoid both allocation and copying.
func isSoft404Mutable(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if hasSpecialRE2CaseFold(body) {
		return soft404Re.Match(body)
	}
	foldASCIIInPlace(body)
	return matchSoft404Lower(body)
}

func hasSpecialRE2CaseFold(body []byte) bool {
	return bytes.Contains(body, longSUTF8) || bytes.Contains(body, kelvinUTF8)
}

func copyFoldASCII(dst, src []byte) {
	for i, c := range src {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst[i] = c
	}
}

func isRE2WordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

func isRE2Whitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func skipRE2WSBack(b []byte, end int) (int, bool) {
	i := end
	for i > 0 && isRE2Whitespace(b[i-1]) {
		i--
	}
	return i, i < end
}

func hasWordBeforeWS(b []byte, end int, words ...[]byte) (int, bool) {
	wordEnd, ok := skipRE2WSBack(b, end)
	if !ok {
		return 0, false
	}
	for _, w := range words {
		if bytes.HasSuffix(b[:wordEnd], w) {
			return wordEnd - len(w), true
		}
	}
	return 0, false
}

func matchSoft404Lower(b []byte) bool {
	for pos := 0; ; {
		i := bytes.Index(b[pos:], tok404)
		if i < 0 {
			break
		}
		at := pos + i
		if (at == 0 || !isRE2WordByte(b[at-1])) && (at+3 == len(b) || !isRE2WordByte(b[at+3])) {
			return true
		}
		pos = at + len(tok404)
	}
	if matchAnchorBeforeWS(b, tokFound, tokNot, tokNothing) {
		return true
	}
	if matchAnchorBeforeWS(b, tokSuch, tokNo) ||
		matchAnchorBeforeWS(b, tokResults, tokNo) ||
		matchAnchorBeforeWS(b, tokURL, tokInvalid) ||
		matchAnchorBeforeWS(b, tokPage, tokUnknown) {
		return true
	}
	for pos := 0; ; {
		i := bytes.Index(b[pos:], tokExist)
		if i < 0 {
			break
		}
		at := pos + i
		if _, ok := hasWordBeforeWS(b, at, tokDoesntQuote, tokDoesnt); ok {
			return true
		}
		if notStart, ok := hasWordBeforeWS(b, at, tokNot); ok {
			if doesStart, ok := hasWordBeforeWS(b, notStart, tokDoes); ok {
				if _, ok := hasWordBeforeWS(b, doesStart, tokPage); ok {
					return true
				}
			}
		}
		pos = at + len(tokExist)
	}
	for pos := 0; ; {
		i := bytes.Index(b[pos:], tokFind)
		if i < 0 {
			break
		}
		at := pos + i
		if _, ok := hasWordBeforeWS(b, at, tokCouldntQ, tokCouldnt); ok {
			return true
		}
		if notStart, ok := hasWordBeforeWS(b, at, tokNot); ok {
			if _, ok := hasWordBeforeWS(b, notStart, tokCould); ok {
				return true
			}
		}
		pos = at + len(tokFind)
	}
	return false
}

func matchAnchorBeforeWS(b, anchor []byte, words ...[]byte) bool {
	for pos := 0; ; {
		i := bytes.Index(b[pos:], anchor)
		if i < 0 {
			return false
		}
		at := pos + i
		if _, ok := hasWordBeforeWS(b, at, words...); ok {
			return true
		}
		pos = at + len(anchor)
	}
}
