// Package curl exports a collection as a shell script of curl commands, one
// per request, in tree order. It writes text only: nothing is executed.
//
// Secrets: like every exporter, the input bundle MUST come from
// store.ExportCollectionsBundle with the default (scrubbed) options. Because
// the scrub blanks literal credentials, a blank value in a credential
// position is written as the placeholder REDACTED so the command stays
// syntactically complete, and the script header says so. {{variable}}
// references are kept verbatim (they are not secrets and are resolved by the
// operator). Variable current values never enter the script.
package curl

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/store"
)

// Placeholder replaces credentials that the scrub blanked.
const Placeholder = "REDACTED"

// ErrNoCollection is returned when the collection uid is not in the bundle.
var ErrNoCollection = errors.New("curl export: collection not found")

// Options for an export.
type Options struct {
	// IncludeSecrets suppresses the placeholder note; the caller must have
	// obtained explicit confirmation and passed an unscrubbed bundle.
	IncludeSecrets bool
	// Insecure adds -k (skip TLS verification), matching how Interseptor
	// itself talks to targets.
	Insecure bool
	// PathAsIs adds --path-as-is so dot segments are not normalised.
	PathAsIs bool
	// Shebang prefixes "#!/bin/sh" so the output is a runnable script.
	Shebang bool
}

// Warning notes content that has no curl equivalent.
type Warning struct {
	Path    string `json:"path,omitempty"`
	Feature string `json:"feature"`
	Message string `json:"message"`
}

// Output is an exported script.
type Output struct {
	Data     []byte    `json:"-"`
	Commands int       `json:"commands"`
	Warnings []Warning `json:"warnings,omitempty"`
}

type exporter struct {
	opt   Options
	warns []Warning
	redac bool
}

// ExportCollection writes every request of collection uid as a curl command.
func ExportCollection(b store.CollectionsBundle, uid string, opt Options) (*Output, error) {
	var col *store.Collection
	for i := range b.Collections {
		if b.Collections[i].UID == uid {
			col = &b.Collections[i]
		}
	}
	if col == nil {
		return nil, ErrNoCollection
	}
	var items []store.Item
	for _, it := range b.Items {
		if it.CollectionUID == uid {
			items = append(items, it)
		}
	}
	e := &exporter{opt: opt}
	var body strings.Builder
	n := 0
	var walk func(nodes []*collection.Node, path string)
	walk = func(nodes []*collection.Node, path string) {
		for _, nd := range nodes {
			p := path + "/" + nd.Name
			if nd.Kind == "folder" {
				body.WriteString("\n# --- " + comment(p) + " ---\n")
				walk(nd.Children, p)
				continue
			}
			body.WriteString("\n# " + comment(p) + "\n")
			body.WriteString(e.command(&nd.Item, p))
			body.WriteString("\n")
			n++
		}
	}
	walk(collection.BuildTree(items), "")
	var head strings.Builder
	if opt.Shebang {
		head.WriteString("#!/bin/sh\n")
	}
	head.WriteString("# Collection: " + comment(col.Name) + "\n")
	head.WriteString("# Exported by Interseptor. {{variables}} are placeholders to substitute before running.\n")
	if e.redac && !opt.IncludeSecrets {
		head.WriteString("# Credentials were scrubbed; replace " + Placeholder + " with real values.\n")
	}
	return &Output{Data: []byte(head.String() + body.String()), Commands: n, Warnings: e.warns}, nil
}

func comment(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

func (e *exporter) warn(path, feature, msg string) {
	e.warns = append(e.warns, Warning{Path: path, Feature: feature, Message: msg})
}

type row struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Disabled    bool   `json:"disabled"`
	Enabled     *bool  `json:"enabled"`
	Type        string `json:"type"`
	ContentType string `json:"contentType"`
}

func (r row) off() bool { return r.Disabled || (r.Enabled != nil && !*r.Enabled) }
func (r row) val() string {
	switch v := r.Value.(type) {
	case nil:
		return ""
	case string:
		return v
	}
	b, _ := json.Marshal(r.Value)
	return string(b)
}

func (e *exporter) command(it *store.Item, path string) string {
	var parts []string
	parts = append(parts, "curl")
	if e.opt.Insecure {
		parts = append(parts, "-k")
	}
	if e.opt.PathAsIs {
		parts = append(parts, "--path-as-is")
	}
	method := strings.ToUpper(strings.TrimSpace(it.Method))
	if method == "" {
		method = "GET"
	}
	if !isToken(method) {
		// curl writes -X verbatim into the request line; a space or line
		// break there would add words or header lines to the request.
		e.warn(path, "invalid-method", "the method is not a valid HTTP token and was omitted")
		method = "GET"
	}
	raw := urlRaw(it.URL)
	if raw == "" {
		e.warn(path, "no-url", "request has no URL")
	}
	var hdrs []row
	_ = json.Unmarshal(it.Headers, &hdrs)
	hasCT := false
	var headerArgs []string
	for _, h := range hdrs {
		if h.off() || strings.TrimSpace(h.Key) == "" {
			continue
		}
		v := h.val()
		if strings.ContainsAny(h.Key+v, "\r\n") {
			// curl sends a line break inside -H as a new header line
			e.warn(path, "header-line-break", "header "+oneLine(h.Key)+" held a line break; it was replaced by a space")
			h.Key, v = oneLine(h.Key), oneLine(v)
		}
		if v == "" && impkit.IsCredentialHeader(h.Key) {
			v = Placeholder
			e.redac = true
		}
		if strings.EqualFold(h.Key, "content-type") {
			hasCT = true
		}
		if strings.TrimSpace(v) == "" {
			// "Name:" tells curl to remove a header; "Name;" sends it empty
			headerArgs = append(headerArgs, "-H "+q(h.Key+";"))
			continue
		}
		headerArgs = append(headerArgs, "-H "+q(h.Key+": "+v))
	}
	authArgs, query := e.auth(it, path)
	if query != "" {
		raw = appendQuery(raw, query)
	}
	bodyArgs, ct := e.body(it, path)
	if ct != "" && !hasCT {
		headerArgs = append(headerArgs, "-H "+q("Content-Type: "+ct))
	}
	hasBody := len(bodyArgs) > 0
	switch {
	case method == "HEAD":
		parts = append(parts, "-I")
	case method == "GET" && !hasBody, method == "POST" && hasBody:
	default:
		parts = append(parts, "-X "+q(method))
	}
	if strings.ContainsAny(withoutRefs(raw), "[]{}") {
		// curl expands [1-3] and {a,b} in a URL into several requests (and
		// rejects brackets it cannot parse); the request sends them literally.
		// {{variable}} references do not count: they are substituted first.
		parts = append(parts, "-g")
	}
	if strings.HasPrefix(raw, "-") {
		// curl would parse a leading '-' as an option
		parts = append(parts, "--url "+q(raw))
	} else {
		parts = append(parts, q(raw))
	}
	parts = append(parts, authArgs...)
	parts = append(parts, headerArgs...)
	parts = append(parts, bodyArgs...)
	return strings.Join(parts, " \\\n  ")
}

func urlRaw(u json.RawMessage) string {
	var s string
	if json.Unmarshal(u, &s) == nil {
		return s
	}
	var o struct {
		Raw string `json:"raw"`
	}
	if json.Unmarshal(u, &o) == nil && o.Raw != "" {
		return o.Raw
	}
	// A structured URL without "raw" is still a URL: the store rebuilds it from
	// protocol, host, port, path and query (disabled rows excluded).
	return store.EffectiveURL(u)
}

func appendQuery(raw, q string) string {
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + q + frag
}

// auth renders the item's own auth (inherited auth lives on folders and the
// collection and is not expanded here; a warning says so when relevant).
func (e *exporter) auth(it *store.Item, path string) (args []string, query string) {
	if len(it.Auth) == 0 {
		return nil, ""
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(it.Auth, &o) != nil {
		return nil, ""
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	typ = strings.ToLower(typ)
	f := authFields(o[typ])
	cred := func(v string) string {
		if v == "" {
			e.redac = true
			return Placeholder
		}
		return v
	}
	switch typ {
	case "", "noauth", "none", "inherit":
	case "bearer":
		args = append(args, "-H "+q("Authorization: Bearer "+cred(f["token"])))
	case "basic":
		args = append(args, "-u "+q(f["username"]+":"+cred(f["password"])))
	case "digest":
		args = append(args, "--digest", "-u "+q(f["username"]+":"+cred(f["password"])))
	case "apikey":
		if strings.EqualFold(f["in"], "query") {
			query = queryEscape(f["key"]) + "=" + queryEscape(cred(f["value"]))
		} else {
			args = append(args, "-H "+q(f["key"]+": "+cred(f["value"])))
		}
	default:
		e.warn(path, "auth:"+typ, "auth type "+typ+" has no curl equivalent and was omitted")
	}
	return args, query
}

func authFields(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	var rows []row
	if json.Unmarshal(raw, &rows) == nil {
		for _, r := range rows {
			out[r.Key] = r.val()
		}
		return out
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			out[k] = row{Value: v}.val()
		}
	}
	return out
}

func (e *exporter) body(it *store.Item, path string) (args []string, ct string) {
	if len(it.Body) == 0 {
		return nil, ""
	}
	var b struct {
		Mode       string          `json:"mode"`
		Raw        string          `json:"raw"`
		URLEncoded []row           `json:"urlencoded"`
		FormData   []row           `json:"formdata"`
		GraphQL    json.RawMessage `json:"graphql"`
		Options    struct {
			Raw struct {
				Language string `json:"language"`
			} `json:"raw"`
		} `json:"options"`
	}
	if json.Unmarshal(it.Body, &b) != nil {
		return nil, ""
	}
	switch strings.ToLower(b.Mode) {
	case "raw":
		if b.Raw == "" {
			return nil, ""
		}
		// The pipeline always derives a content type for a raw body; curl
		// would send its own form default for --data-raw, so write it out.
		return []string{"--data-raw " + q(b.Raw)}, rawContentType(b.Options.Raw.Language)
	case "urlencoded":
		for _, r := range b.URLEncoded {
			switch {
			case r.off() || r.Key == "": // the pipeline skips a row without a name
			case plainName(r.Key):
				args = append(args, "--data-urlencode "+q(r.Key+"="+r.val()))
			default:
				// --data-urlencode encodes the value only and splits at the
				// first '=', so a name holding '=', '&', '+', '%' or a space is
				// written already encoded.
				args = append(args, "--data-raw "+q(escapeKeepRefs(r.Key)+"="+escapeKeepRefs(r.val())))
			}
		}
		return args, ""
	case "formdata":
		for _, r := range b.FormData {
			if !r.off() {
				args = append(args, e.formField(r, path)...)
			}
		}
		return args, ""
	case "graphql":
		var g struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		_ = json.Unmarshal(b.GraphQL, &g)
		payload := map[string]any{"query": g.Query}
		if g.Variables != nil {
			if s, ok := g.Variables.(string); ok {
				var j any
				if json.Unmarshal([]byte(s), &j) == nil {
					payload["variables"] = j
				}
			} else {
				payload["variables"] = g.Variables
			}
		}
		return []string{"--data-raw " + q(string(impkit.MustJSON(payload)))}, "application/json"
	case "file":
		e.warn(path, "needs-asset", "file body; replace FILE_PATH with a local path")
		return []string{"--data-binary " + q("@FILE_PATH")}, ""
	case "", "none":
		return nil, ""
	}
	e.warn(path, "body:"+b.Mode, "body mode "+b.Mode+" has no curl equivalent and was omitted")
	return nil, ""
}

// formField renders one enabled form-data row. Imported values are untrusted:
// with -F, curl reads local files for a value starting with @ or <, and
// parses ;headers=@file style parameters, so text goes through --form-string
// (taken literally). A content type needs -F; the value is then a curl
// double-quoted word (no file forms, ';' is literal) and the type is cut at
// its first parameter separator. A name containing '=' cannot be expressed
// with -F (curl splits at the first '=') and is skipped.
func (e *exporter) formField(r row, path string) []string {
	if (r.Type == "file" || r.ContentType != "") && strings.Contains(r.Key, "=") {
		e.warn(path, "form-name", "form field name "+r.Key+" contains '=' and was omitted")
		return nil
	}
	if r.Type == "file" {
		e.warn(path, "needs-asset", "form field "+r.Key+" uploads a file; replace FILE_PATH with a local path")
		return []string{"-F " + q(r.Key+"=@FILE_PATH")}
	}
	if r.ContentType == "" {
		return []string{"--form-string " + q(r.Key+"="+r.val())}
	}
	ct := r.ContentType
	if i := strings.IndexAny(ct, ";,\r\n"); i >= 0 {
		e.warn(path, "form-type", "form field "+r.Key+": content type parameters were dropped")
		ct = strings.TrimSpace(ct[:i])
	}
	v := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(r.val())
	return []string{"-F " + q(r.Key+`="`+v+`";type=`+ct)}
}

// isToken reports an RFC 9110 token (the only form a method can take).
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			return false
		}
	}
	return true
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s))
}

// plainName reports a form field name curl can take as written: no character
// that --data-urlencode would leave unencoded or read as a separator.
func plainName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && c != '_' && c != '-' && c != '.' && c != '~' {
			return false
		}
	}
	return s != ""
}

// queryEscape mirrors the pipeline's query escaping (collexec.queryEscape):
// bytes that cannot appear in a query component are percent-encoded, existing
// %XX escapes and URL-safe punctuation are left alone, so the script puts the
// same bytes on the wire as a send. {{variable}} references stay readable.
func queryEscape(s string) string {
	const safe = "-._~!$'()*,;:@/?+"
	hex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '{' && strings.HasPrefix(s[i:], "{{") {
			if j := strings.Index(s[i:], "}}"); j >= 0 {
				sb.WriteString(s[i : i+j+2])
				i += j + 1
				continue
			}
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(safe, c) >= 0:
			sb.WriteByte(c)
		case c == '%' && i+2 < len(s) && hex(s[i+1]) && hex(s[i+2]):
			sb.WriteByte(c)
		default:
			sb.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+0x100, 16)[1:]))
		}
	}
	return sb.String()
}

// withoutRefs removes {{variable}} references from s.
func withoutRefs(s string) string {
	var sb strings.Builder
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			break
		}
		sb.WriteString(s[:i])
		s = s[i+j+2:]
	}
	sb.WriteString(s)
	return sb.String()
}

// escapeKeepRefs form-encodes s but leaves {{variable}} references readable,
// so the operator can still substitute them in the script.
func escapeKeepRefs(s string) string {
	var sb strings.Builder
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			break
		}
		sb.WriteString(url.QueryEscape(s[:i]))
		sb.WriteString(s[i : i+j+2])
		s = s[i+j+2:]
	}
	sb.WriteString(url.QueryEscape(s))
	return sb.String()
}

// q single-quotes s for a POSIX shell.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// rawContentType mirrors the pipeline's default content type for a raw body
// language (collexec.defaultContentType): an exported command has to send the
// same bytes with the same type, and curl's --data-raw default is a form type.
func rawContentType(lang string) string {
	switch strings.ToLower(lang) {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "html":
		return "text/html"
	case "javascript":
		return "application/javascript"
	}
	return "text/plain"
}
