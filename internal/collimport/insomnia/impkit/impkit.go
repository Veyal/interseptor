// Package impkit holds the helpers shared by the Insomnia, Bruno and HAR
// importers: Postman-shaped column builders (url object, headers, auth),
// script quarantine recording and report finishing. Nothing here executes,
// fetches or reads anything; it only shapes data.
package impkit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
)

// Report types are shared with the Postman importer so the UI renders every
// import the same way.
type (
	Report = postman.Report
	Entry  = postman.Entry
	Level  = postman.Level
)

const (
	Converted      = postman.Converted
	Degraded       = postman.Degraded
	PreservedInert = postman.PreservedInert
	Unsupported    = postman.Unsupported
	Blocked        = postman.Blocked
	NeedsReview    = postman.NeedsReview
)

// Bounds shared by the importers.
const (
	MaxInputBytes = 64 << 20
	MaxDepth      = 64
	MaxItems      = 100000
	MaxScriptSize = 512 * 1024
	MaxURLLen     = 64 << 10
)

// MustJSON marshals without HTML escaping; failures yield nil.
func MustJSON(v any) json.RawMessage {
	b, err := postman.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// Row is one ordered key/value row (header, query param, form field).
type Row struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Disabled    bool   `json:"disabled,omitempty"`
	Type        string `json:"type,omitempty"`
	Src         string `json:"src,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// Rows marshals rows as a JSON array (never null).
func Rows(rs []Row) json.RawMessage {
	if rs == nil {
		rs = []Row{}
	}
	return MustJSON(rs)
}

// AuthObj builds {"type":t,"<t>":[{key,value,type:string}...]}.
func AuthObj(typ string, kv [][2]string) json.RawMessage {
	m := postman.NewOMap()
	m.SetValue("type", typ)
	rows := make([]map[string]string, 0, len(kv))
	for _, p := range kv {
		rows = append(rows, map[string]string{"key": p[0], "value": p[1], "type": "string"})
	}
	if typ != "noauth" {
		m.SetValue(typ, rows)
	}
	return MustJSON(m)
}

// RawBody builds a raw body with an optional editor language.
func RawBody(text, lang string) json.RawMessage {
	m := postman.NewOMap()
	m.SetValue("mode", "raw")
	m.SetValue("raw", text)
	if lang != "" {
		m.SetValue("options", map[string]any{"raw": map[string]string{"language": lang}})
	}
	return MustJSON(m)
}

// LanguageFor maps a content type to a raw-body language.
func LanguageFor(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "json"):
		return "json"
	case strings.Contains(ct, "xml"):
		return "xml"
	case strings.Contains(ct, "html"):
		return "html"
	case strings.Contains(ct, "javascript"):
		return "javascript"
	case strings.HasPrefix(ct, "text/"):
		return "text"
	}
	return ""
}

// PathVar is a ":name" path variable row.
type PathVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// URLObject builds a Postman-style structured url from a raw URL without
// decoding anything (variables such as {{baseUrl}} survive untouched).
func URLObject(raw string, pathVars []PathVar) json.RawMessage {
	return URLObjectWith(raw, pathVars, nil)
}

// URLObjectWith is URLObject plus extra query rows (typically disabled ones
// that are not part of the raw URL) appended to the query array.
func URLObjectWith(raw string, pathVars []PathVar, extra []Row) json.RawMessage {
	if len(raw) > MaxURLLen {
		raw = raw[:MaxURLLen]
	}
	m := postman.NewOMap()
	m.SetValue("raw", raw)
	rest, hash, query, hasQuery := raw, "", "", false
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, hash = rest[:i], rest[i+1:]
	}
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
	if hasQuery || len(extra) > 0 {
		q := queryRows(query)
		q = append(q, extra...)
		m.SetValue("query", q)
	}
	if hash != "" {
		m.SetValue("hash", hash)
	}
	if len(pathVars) > 0 {
		m.SetValue("variable", pathVars)
	}
	return MustJSON(m)
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

func splitHost(h string) []string {
	if strings.Contains(h, "{{") {
		return []string{h}
	}
	return strings.Split(h, ".")
}

func queryRows(q string) []Row {
	rows := []Row{}
	for _, p := range strings.Split(q, "&") {
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		rows = append(rows, Row{Key: k, Value: v})
	}
	return rows
}

// AppendQuery adds query rows to a raw URL (before any fragment).
func AppendQuery(raw string, rows []Row) string {
	var parts []string
	for _, r := range rows {
		if r.Disabled || r.Key == "" && r.Value == "" {
			continue
		}
		if r.Value == "" {
			parts = append(parts, r.Key)
		} else {
			parts = append(parts, r.Key+"="+r.Value)
		}
	}
	if len(parts) == 0 {
		return raw
	}
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
		if strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "&") {
			sep = ""
		}
	}
	return raw + sep + strings.Join(parts, "&") + frag
}

// HostAndPath returns "host/path" for naming an item.
func HostAndPath(raw string) string {
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

// Clip truncates to n bytes on a rune boundary.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
		s = s[:len(s)-1]
	}
	// drop a possibly split final rune
	if len(s) > 0 && s[len(s)-1] >= 0xC0 {
		s = s[:len(s)-1]
	}
	return s
}

var credHeader = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|cookie|x-api-key|api-key|x-auth-token|x-access-token|x-csrf-token)$|(?i)(token|secret|api[-_]?key)`)
var secretKey = regexp.MustCompile(`(?i)(secret|password|passwd|token|api[-_]?key|apikey|private[-_]?key|credential|authorization|bearer)`)

// IsCredentialHeader reports whether a header name usually carries a secret.
func IsCredentialHeader(name string) bool { return credHeader.MatchString(name) }

// IsSecretKey reports whether a variable name looks like it holds a secret.
func IsSecretKey(name string) bool { return secretKey.MatchString(name) }

// CountCredential records an embedded credential without its value.
func CountCredential(rep *Report, path, uid, what string) {
	rep.Stats.EmbeddedCredentials++
	rep.Entries = append(rep.Entries, Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "embedded-credential",
		Message: what + " holds a literal credential (value not shown)", Suggestion: "Lift it to a secret variable and reference it as {{name}}"})
}

// ScanHeaderCredentials flags literal credential headers.
func ScanHeaderCredentials(rep *Report, path, uid string, rows []Row) {
	for _, h := range rows {
		if h.Disabled || h.Value == "" || strings.Contains(h.Value, "{{") {
			continue
		}
		if IsCredentialHeader(h.Key) {
			CountCredential(rep, path, uid, "header "+h.Key)
		}
	}
}

var (
	scriptAPI  = regexp.MustCompile(`\b(pm|bru|req|res|insomnia|postman)\.([A-Za-z_]+)`)
	scriptHost = regexp.MustCompile(`https?://([A-Za-z0-9][A-Za-z0-9.\-]*)`)
	scriptEval = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(|\bFunction\s*\(`)
	scriptHex  = regexp.MustCompile(`\\x[0-9a-fA-F]{2}`)
	scriptObf  = regexp.MustCompile(`\b_0x[0-9a-f]{4,}\b`)
	scriptBad  = regexp.MustCompile(`\bfetch\s*\(|\bnew\s+XMLHttpRequest\b|\bprocess\.(env|exit|argv|binding)|\bchild_process\b|\brequire\(\s*['"](fs|child_process|net|http|https|os)['"]`)
)

// ScriptEvent builds a Postman-shaped event for a script. dialect marks the
// source language family so the runner can pick the right shim.
func ScriptEvent(listen, dialect, src string) json.RawMessage {
	m := postman.NewOMap()
	m.SetValue("listen", listen)
	s := postman.NewOMap()
	s.SetValue("type", "text/javascript")
	s.SetValue("exec", strings.Split(src, "\n"))
	m.SetValue("script", s)
	if dialect != "" {
		m.SetValue("dialect", dialect)
	}
	return MustJSON(m)
}

// Events joins event rows into an events array (nil when empty).
func Events(evs []json.RawMessage) json.RawMessage {
	if len(evs) == 0 {
		return nil
	}
	return postman.MarshalArray(evs)
}

// RecordScript statically analyses a quarantined script (it never runs) and
// adds the scripts-panel row and report entries. dialect is "bruno",
// "insomnia" or "postman".
func RecordScript(rep *Report, path, uid, listen, dialect, src string) {
	sum := sha256.Sum256([]byte(src))
	hash := hex.EncodeToString(sum[:])
	lines := strings.Split(src, "\n")
	apis, hosts := map[string]bool{}, map[string]bool{}
	unsupported := map[string]bool{}
	pmOnly := true
	for _, ln := range lines {
		for _, m := range scriptAPI.FindAllStringSubmatch(ln, -1) {
			apis[m[1]+"."+m[2]] = true
			if m[1] != "pm" && m[1] != "postman" {
				pmOnly = false
			}
		}
		for _, m := range scriptBad.FindAllString(ln, -1) {
			unsupported[strings.TrimSpace(strings.TrimSuffix(m, "("))] = true
		}
		for _, m := range scriptHost.FindAllStringSubmatch(ln, -1) {
			hosts[strings.ToLower(m[1])] = true
		}
	}
	var flags []string
	if scriptEval.MatchString(src) {
		flags = append(flags, "eval-or-Function")
	}
	if len(scriptHex.FindAllString(src, -1)) >= 8 || scriptObf.MatchString(src) {
		flags = append(flags, "obfuscation")
	}
	if len(src) > MaxScriptSize {
		flags = append(flags, "oversize")
	}
	status := "partial"
	switch {
	case len(unsupported) > 0:
		status = "unsupported"
	case len(apis) > 0 && !pmOnly:
		// bru.* / insomnia.* / req.* / res.* have no shim in this build.
		status = "unsupported"
	case len(apis) == 0:
		status = "supported"
	}
	list := sortedKeys(apis)
	rep.ScriptList = append(rep.ScriptList, postman.ScriptInfo{Path: path, Item: uid, Listen: listen, SourceHash: hash,
		Lines: len(lines), APIs: list, Hosts: sortedKeys(hosts), Flags: flags, Status: status, Quarantine: true})
	rep.Scripts.Total++
	switch status {
	case "supported":
		rep.Scripts.Supported++
	case "partial":
		rep.Scripts.Partial++
	default:
		rep.Scripts.Unsupported++
	}
	rep.Entries = append(rep.Entries, Entry{Level: Blocked, Path: path, Item: uid, Feature: "script-quarantined",
		Message:    fmt.Sprintf("%s %s script (%d lines, %s) will not run until the owner trusts it in the UI", dialect, listen, len(lines), status),
		Suggestion: "Review the source, hash " + hash[:12] + ", then trust it"})
	if status == "unsupported" {
		what := strings.Join(list, ", ")
		if what == "" {
			what = strings.Join(sortedKeys(unsupported), ", ")
		}
		rep.Entries = append(rep.Entries, Entry{Level: Unsupported, Path: path, Item: uid, Feature: "script-api:" + dialect,
			Message: dialect + " script API (" + what + ") has no shim in this build; the script reports 'unsupported' at run time instead of a false pass"})
	}
	for _, f := range flags {
		rep.Entries = append(rep.Entries, Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "script-flag:" + f,
			Message: "script flagged: " + f, Suggestion: "Read the source carefully before trusting"})
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func levelRank(l Level) int {
	switch l {
	case Unsupported:
		return 0
	case Blocked:
		return 1
	case NeedsReview:
		return 2
	case Degraded:
		return 3
	case PreservedInert:
		return 4
	}
	return 5
}

// Finish computes counts, orders entries worst-first and writes the headline.
func Finish(r *Report, label string) {
	r.Counts = map[Level]int{}
	for _, e := range r.Entries {
		r.Counts[e.Level]++
	}
	sort.SliceStable(r.Entries, func(i, j int) bool { return levelRank(r.Entries[i].Level) < levelRank(r.Entries[j].Level) })
	s := r.Scripts
	r.Headline = fmt.Sprintf("%d folder(s), %d request(s) from %s; %d scripts: %d fully supported, %d partial, %d using unsupported APIs (all quarantined until trusted); %d need review, %d degraded, %d unsupported",
		r.Stats.Folders, r.Stats.Requests, label, s.Total, s.Supported, s.Partial, s.Unsupported,
		r.Counts[NeedsReview], r.Counts[Degraded], r.Counts[Unsupported])
}
