package sender

import (
	"net/url"
	"strings"
)

// Param is one ordered query or x-www-form-urlencoded field. Unlike a map,
// a slice of Params keeps field order and repeated keys, which a lossless
// Repeater params editor needs.
type Param struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ParseParams splits an application/x-www-form-urlencoded string (a URL query
// string or a form body) into ordered fields, decoding percent-escapes and
// "+". Order and duplicate keys are preserved; empty "&&" segments are dropped.
// A bare "key" (no "=") becomes {key, ""}.
func ParseParams(raw string) []Param {
	raw = strings.TrimPrefix(raw, "?")
	if raw == "" {
		return nil
	}
	var out []Param
	for _, seg := range strings.Split(raw, "&") {
		if seg == "" {
			continue
		}
		key, val, _ := strings.Cut(seg, "=")
		out = append(out, Param{Key: unescapeParam(key), Value: unescapeParam(val)})
	}
	return out
}

// BuildParams serialises ordered fields back to a form-urlencoded string,
// percent-escaping keys and values (space as "+"). ParseParams(BuildParams(p))
// equals ParseParams(ParseParams-of(p)): the round trip preserves order,
// duplicates and decoded values.
func BuildParams(params []Param) string {
	if len(params) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.Value))
	}
	return b.String()
}

// unescapeParam decodes a form-urlencoded token, falling back to the raw text
// when it is not valid percent-encoding so no field is ever dropped.
func unescapeParam(s string) string {
	if s == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(s); err == nil {
		return dec
	}
	return s
}
