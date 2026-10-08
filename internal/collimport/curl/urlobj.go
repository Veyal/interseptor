package curl

import (
	"encoding/json"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
)

// urlObject builds a Postman-style structured url from a raw URL without
// decoding anything (variables such as {{baseUrl}} survive untouched).
func urlObject(raw string) json.RawMessage {
	m := postman.NewOMap()
	m.SetValue("raw", raw)
	rest := raw
	hash := ""
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, hash = rest[:i], rest[i+1:]
	}
	query := ""
	hasQuery := false
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query, hasQuery = rest[:i], rest[i+1:], true
	}
	if i := strings.Index(rest, "://"); i > 0 && isScheme(rest[:i]) {
		m.SetValue("protocol", rest[:i])
		rest = rest[i+3:]
	}
	auth, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		auth, path = rest[:i], rest[i+1:]
	}
	if i := strings.LastIndexByte(auth, '@'); i >= 0 {
		auth = auth[i+1:]
	}
	host, port := splitPort(auth)
	if host != "" {
		m.SetValue("host", splitHost(host))
	}
	if port != "" {
		m.SetValue("port", port)
	}
	if path != "" {
		m.SetValue("path", strings.Split(path, "/"))
	}
	if hasQuery {
		m.SetValue("query", queryRows(query))
	}
	if hash != "" {
		m.SetValue("hash", hash)
	}
	return mustJSON(m)
}

func isScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'))) {
			return false
		}
	}
	return s != ""
}

func splitPort(auth string) (host, port string) {
	i := strings.LastIndexByte(auth, ':')
	if i < 0 || strings.HasSuffix(auth, "]") {
		return auth, ""
	}
	p := auth[i+1:]
	if p == "" {
		return auth, ""
	}
	for j := 0; j < len(p); j++ {
		if p[j] < '0' || p[j] > '9' {
			return auth, ""
		}
	}
	return auth[:i], p
}

// splitHost splits a host on dots but keeps {{var}} references whole.
func splitHost(h string) []string {
	if strings.Contains(h, "{{") {
		return []string{h}
	}
	return strings.Split(h, ".")
}

type queryRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func queryRows(q string) []queryRow {
	rows := []queryRow{}
	for _, p := range strings.Split(q, "&") {
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		rows = append(rows, queryRow{Key: k, Value: v})
	}
	return rows
}

// hostAndPath returns "host/path" for naming an item.
func hostAndPath(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i > 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 && i < strings.IndexByte(s+"/", '/') {
		s = s[i+1:]
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := postman.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
