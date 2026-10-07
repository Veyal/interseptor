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
	method := strings.ToUpper(it.Method)
	if method == "" {
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
		if h.off() || h.Key == "" {
			continue
		}
		v := h.val()
		if v == "" && impkit.IsCredentialHeader(h.Key) {
			v = Placeholder
			e.redac = true
		}
		if strings.EqualFold(h.Key, "content-type") {
			hasCT = true
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
	parts = append(parts, q(raw))
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
		Raw   string `json:"raw"`
		Query []row  `json:"query"`
	}
	if json.Unmarshal(u, &o) != nil {
		return ""
	}
	return o.Raw
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
			query = url.QueryEscape(f["key"]) + "=" + cred(f["value"])
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
		switch b.Options.Raw.Language {
		case "json":
			ct = "application/json"
		case "xml":
			ct = "application/xml"
		}
		return []string{"--data-raw " + q(b.Raw)}, ct
	case "urlencoded":
		for _, r := range b.URLEncoded {
			if !r.off() {
				args = append(args, "--data-urlencode "+q(r.Key+"="+r.val()))
			}
		}
		return args, ""
	case "formdata":
		for _, r := range b.FormData {
			if r.off() {
				continue
			}
			if r.Type == "file" {
				e.warn(path, "needs-asset", "form field "+r.Key+" uploads a file; replace FILE_PATH with a local path")
				args = append(args, "-F "+q(r.Key+"=@FILE_PATH"))
				continue
			}
			v := r.Key + "=" + r.val()
			if r.ContentType != "" {
				v += ";type=" + r.ContentType
			}
			args = append(args, "-F "+q(v))
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

// q single-quotes s for a POSIX shell.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
