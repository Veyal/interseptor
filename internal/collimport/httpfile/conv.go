package httpfile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

type conv struct {
	res     *postman.Result
	opt     Options
	dialect string
	prev    string
	defined map[string]int    // file variable -> index in res.Variables
	refs    map[string]string // referenced plain variable -> first item name
	order   []string
}

func (c *conv) add(l impkit.Level, path, uid, feature, msg, sugg string) {
	c.res.Report.Entries = append(c.res.Report.Entries, impkit.Entry{Level: l, Path: path, Item: uid, Feature: feature, Message: msg, Suggestion: sugg})
}

// block is one ###-delimited section.
type block struct {
	name  string
	start int // 1-based line number of the first content line
	lines []string
}

func splitBlocks(text string) []block {
	var out []block
	cur := block{start: 1}
	for i, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "###") {
			out = append(out, cur)
			cur = block{name: strings.TrimSpace(strings.TrimLeft(ln, "#")), start: i + 2}
			continue
		}
		cur.lines = append(cur.lines, ln)
	}
	return append(out, cur)
}

func (c *conv) run(text string) {
	blocks := splitBlocks(text)
	c.add(impkit.Converted, "", "", "dialect", "detected dialect: "+c.dialect+" (vscode, jetbrains or common; both are parsed with the same rules)", "")
	c.res.Report.Stats.Folders = 0
	for _, b := range blocks {
		if c.res.Report.Stats.Requests >= MaxRequests {
			if blockHasRequest(b) {
				c.add(impkit.Blocked, "", "", "too-many-requests", fmt.Sprintf("only the first %d requests were imported", MaxRequests), "")
				break
			}
			continue
		}
		c.block(b)
	}
}

func blockHasRequest(b block) bool {
	for _, ln := range b.lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "@") || strings.HasPrefix(t, "<") {
			continue
		}
		return true
	}
	return false
}

var (
	directiveLineRe = regexp.MustCompile(`^(?:#|//)\s*@([A-Za-z][\w-]*)(?:\s+(.*))?$`)
	varLineRe       = regexp.MustCompile(`^@([^\s=]+)\s*=\s*(.*)$`)
	headerRe        = regexp.MustCompile("^([A-Za-z0-9!#$%&'*+.^_`|~-]+)\\s*:\\s*(.*)$")
	versionRe       = regexp.MustCompile(`(?i)\s+HTTP/\d(?:\.\d)?$`)
	urlStartRe      = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*://|/|\{\{)`)
	bodyIncludeRe   = regexp.MustCompile(`^<(@\S*)?\s+(\S.*)$`)
)

var methods = map[string]bool{}

func init() {
	for _, m := range strings.Fields("GET POST PUT PATCH DELETE HEAD OPTIONS TRACE CONNECT PROPFIND PROPPATCH MKCOL COPY MOVE LOCK UNLOCK REPORT SEARCH PURGE LINK UNLINK MKACTIVITY CHECKOUT MERGE NOTIFY SUBSCRIBE UNSUBSCRIBE MKCALENDAR VIEW") {
		methods[m] = true
	}
}

// script is a captured handler / pre-request script.
type script struct {
	listen string // prerequest | test
	src    string
}

// req is the raw parse of one block before conversion.
type req struct {
	name       string
	method     string
	url        string
	headers    []impkit.Row
	body       []string
	directives map[string]string
	scripts    []script
	assets     []asset // handler files
	notes      []note
	hostOnly   bool
	line       int
}

type asset struct{ kind, path string }
type note struct {
	level   impkit.Level
	feature string
	msg     string
	sugg    string
}

func (r *req) note(l impkit.Level, f, m, s string) { r.notes = append(r.notes, note{l, f, m, s}) }

// collectScript reads a `{% ... %}` block starting at lines[i] (which holds
// the opening `{%` after the leading < or >). It returns the script source and
// the index of the last consumed line.
func collectScript(lines []string, i int) (string, int) {
	first := lines[i]
	k := strings.Index(first, "{%")
	rest := first[k+2:]
	if e := strings.Index(rest, "%}"); e >= 0 {
		return strings.TrimSpace(rest[:e]), i
	}
	parts := []string{rest}
	for j := i + 1; j < len(lines); j++ {
		if e := strings.Index(lines[j], "%}"); e >= 0 {
			parts = append(parts, lines[j][:e])
			return dedent(parts), j
		}
		parts = append(parts, lines[j])
	}
	return dedent(parts), len(lines) - 1
}

func dedent(parts []string) string {
	for len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}
	for len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	min := -1
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		n := len(p) - len(strings.TrimLeft(p, " \t"))
		if min < 0 || n < min {
			min = n
		}
	}
	for i, p := range parts {
		if len(p) >= min && min > 0 {
			parts[i] = p[min:]
		}
	}
	return strings.Join(parts, "\n")
}

func (c *conv) block(b block) {
	r := &req{name: b.name, directives: map[string]string{}}
	lines := b.lines
	i := 0
	// preamble: comments, directives, @var definitions, pre-request scripts
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			continue
		case strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//"):
			if m := directiveLineRe.FindStringSubmatch(t); m != nil {
				r.directives[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
			}
			continue
		case varLineRe.MatchString(t):
			m := varLineRe.FindStringSubmatch(t)
			c.defineVar(m[1], strings.TrimSpace(m[2]))
			continue
		case strings.HasPrefix(t, "@"):
			if f := strings.Fields(t); len(f) >= 2 && f[0] == "@name" {
				r.directives["name"] = strings.Join(f[1:], " ")
			}
			continue
		case strings.HasPrefix(t, "<") && strings.Contains(t, "{%") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(t, "<")), "{%"):
			src, end := collectScript(lines, i)
			r.scripts = append(r.scripts, script{"prerequest", src})
			i = end
			continue
		}
		break
	}
	if i >= len(lines) {
		return
	}
	line := b.start + i
	r.line = line
	first := strings.TrimSpace(lines[i])
	// URL continuation on following indented lines
	j := i + 1
	for j < len(lines) && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t') && strings.TrimSpace(lines[j]) != "" {
		part := strings.TrimSpace(lines[j])
		if strings.HasPrefix(strings.ToUpper(part), "HTTP/") {
			part = " " + part
		}
		first += part
		j++
	}
	if !c.requestLine(r, first, line) {
		return
	}
	// headers
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			j++
			break
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			if m := directiveLineRe.FindStringSubmatch(t); m != nil {
				r.directives[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
			}
			continue
		}
		if m := headerRe.FindStringSubmatch(t); m != nil {
			r.headers = append(r.headers, impkit.Row{Key: m[1], Value: strings.TrimSpace(m[2])})
			continue
		}
		break // no blank line: the body starts here
	}
	c.bodyLines(r, lines[min(j, len(lines)):])
	c.emit(r)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// requestLine fills method/url; it reports false (and a report entry) when
// the line is not a request.
func (c *conv) requestLine(r *req, ln string, line int) bool {
	method, rest := "", ln
	if f := strings.Fields(ln); len(f) > 0 && methods[f[0]] {
		method = f[0]
		rest = strings.TrimSpace(strings.TrimPrefix(ln, f[0]))
	} else if len(f) > 0 && isUnsupportedMethod(f[0]) {
		c.add(impkit.Unsupported, "", "", "request-type:"+strings.ToLower(f[0]), fmt.Sprintf("line %d: %s requests are not supported (HTTP only); the request was skipped", line, f[0]), "")
		return false
	}
	rest = strings.TrimSpace(versionRe.ReplaceAllString(rest, ""))
	if method == "" {
		if !urlStartRe.MatchString(rest) || strings.ContainsAny(rest, " \t") {
			c.add(impkit.Degraded, "", "", "unrecognized-line", fmt.Sprintf("line %d is not a request line and the block was skipped", line), "")
			return false
		}
		method = "GET"
	}
	if rest == "" {
		c.add(impkit.Degraded, "", "", "unrecognized-line", fmt.Sprintf("line %d has a method but no URL and was skipped", line), "")
		return false
	}
	r.method, r.url = method, rest
	return true
}

func isUnsupportedMethod(s string) bool {
	switch s {
	case "GRAPHQL", "WEBSOCKET", "GRPC", "WS", "WSS":
		return true
	}
	return false
}

// bodyLines splits the trailing section into body text, response handlers,
// response redirects and handler-file references.
func (c *conv) bodyLines(r *req, lines []string) {
	var body []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(t, ">") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(t, ">")), "{%"):
			src, end := collectScript(lines, i)
			r.scripts = append(r.scripts, script{"test", src})
			i = end
		case strings.HasPrefix(t, ">>"):
			r.note(impkit.Degraded, "response-redirect", "response redirect (>> file) is not performed: responses are not written to disk", "")
		case strings.HasPrefix(t, "<>"):
			r.note(impkit.Degraded, "response-history", "response history reference (<> file) ignored", "")
		case strings.HasPrefix(t, ">") && len(t) > 1 && (t[1] == ' ' || t[1] == '\t'):
			r.assets = append(r.assets, asset{"handler", strings.TrimSpace(t[1:])})
		default:
			body = append(body, lines[i])
		}
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	r.body = body
}

// defineVar records a file-level @name = value variable.
func (c *conv) defineVar(name, val string) {
	val = c.rewrite(val, "@"+name, "", false)
	sv := store.Variable{OwnerKind: store.VarOwnerCollection, OwnerUID: c.res.Collection.UID, Key: name, Type: store.VarTypeDefault, InitialValue: val, Enabled: true}
	if impkit.IsSecretKey(name) && val != "" && !strings.Contains(val, "{{") {
		sv.Type, sv.InitialValue = store.VarTypeSecret, ""
		c.res.SecretValues = append(c.res.SecretValues, postman.SecretValue{OwnerKind: sv.OwnerKind, OwnerUID: sv.OwnerUID, Key: name, Value: val})
		c.res.Report.Stats.SecretVariables++
		c.add(impkit.NeedsReview, "variables", "", "secret-variable", "variable "+name+" holds a secret value; it was moved out of the shareable initial value (offered as a local current value only)", "")
	}
	if idx, ok := c.defined[name]; ok {
		c.res.Variables[idx] = sv
		c.add(impkit.Degraded, "variables", "", "duplicate-variable", "variable "+name+" is defined more than once; the last definition wins", "")
		return
	}
	c.defined[name] = len(c.res.Variables)
	c.res.Report.Stats.Variables++
	c.res.Variables = append(c.res.Variables, sv)
}

var settingsMsgs = map[string]string{
	"no-cookie-jar":      "cookie jar is not used; Interseptor does not persist cookies per request",
	"no-log":             "response logging flag has no equivalent",
	"note":               "confirmation prompt (@note) is not supported; the request sends without confirmation",
	"connection-timeout": "connection timeout is not supported separately",
}

func (c *conv) emit(r *req) {
	it := store.Item{UID: c.opt.NewID(), CollectionUID: c.res.Collection.UID, Kind: "request", Rank: store.RankBetween(c.prev, "")}
	c.prev = it.Rank
	it.Method = r.method
	rawURL := r.url
	// bare path: build the URL from Host
	if strings.HasPrefix(rawURL, "/") {
		host := ""
		var kept []impkit.Row
		for _, h := range r.headers {
			if strings.EqualFold(h.Key, "Host") && host == "" {
				host = h.Value
				continue
			}
			kept = append(kept, h)
		}
		if host != "" {
			scheme := "http"
			if strings.HasSuffix(host, ":443") {
				scheme = "https"
			}
			rawURL = scheme + "://" + host + rawURL
			r.headers = kept
			r.note(impkit.Degraded, "host-header-folded", "request path combined with the Host header into a full URL ("+scheme+" assumed); the Host header was folded into the URL", "Check the scheme")
		} else {
			r.note(impkit.NeedsReview, "no-host", "request has a bare path and no Host header; the URL has no host", "Add a host")
		}
	} else if !strings.Contains(rawURL, "://") && !strings.HasPrefix(rawURL, "{{") {
		rawURL = "http://" + rawURL
		r.note(impkit.Degraded, "scheme-assumed", "URL had no scheme; http:// was assumed", "")
	}
	if len(rawURL) > impkit.MaxURLLen {
		rawURL = impkit.Clip(rawURL, impkit.MaxURLLen)
		r.note(impkit.Degraded, "url-truncated", "URL longer than 64 KiB was truncated", "")
	}
	name := r.directives["name"]
	if name == "" {
		name = r.name
	}
	if name == "" {
		name = r.method + " " + pathOf(rawURL)
	}
	it.Name = strings.TrimSpace(impkit.Clip(name, 120))
	itemPath := it.Name
	rawURL = c.rewrite(rawURL, itemPath, it.UID, true)
	for i := range r.headers {
		r.headers[i].Value = c.rewrite(r.headers[i].Value, itemPath, it.UID, true)
	}
	it.URL = impkit.URLObject(rawURL, nil)
	c.body(r, &it, itemPath)
	it.Headers = impkit.Rows(r.headers)
	c.directivesTo(r, &it, itemPath)
	var evs []json.RawMessage
	for _, s := range r.scripts {
		evs = append(evs, impkit.ScriptEvent(s.listen, "jetbrains", s.src))
		c.recordScript(itemPath, it.UID, s)
	}
	it.Events = impkit.Events(evs)
	for _, a := range r.assets {
		c.res.Report.Stats.NeedsAsset++
		c.add(impkit.NeedsReview, itemPath, it.UID, "needs-asset", "response handler file "+a.path+" is referenced but never read at import; the handler is not imported", "Paste the script into the request or attach it")
	}
	impkit.ScanHeaderCredentials(&c.res.Report, itemPath, it.UID, r.headers)
	for n := store.CountURLBodyCredentials(it.URL, it.Body); n > 0; n-- {
		impkit.CountCredential(&c.res.Report, itemPath, it.UID, "URL query or body")
	}
	for _, n := range r.notes {
		c.add(n.level, itemPath, it.UID, n.feature, n.msg, n.sugg)
	}
	it.Sidecar = impkit.MustJSON(postman.ItemSidecar{Extra: map[string]json.RawMessage{"dialect": impkit.MustJSON(c.dialect), "line": impkit.MustJSON(r.line)}})
	c.res.Items = append(c.res.Items, it)
	c.res.Report.Stats.Requests++
}

func pathOf(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i > 0 {
		s = s[i+3:]
		if k := strings.IndexByte(s, '/'); k >= 0 {
			s = s[k:]
		} else {
			s = "/"
		}
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		s = "/"
	}
	return s
}

func (c *conv) directivesTo(r *req, it *store.Item, itemPath string) {
	s := postman.NewOMap()
	has := false
	keys := make([]string, 0, len(r.directives))
	for k := range r.directives {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := r.directives[k]
		switch k {
		case "name":
		case "no-redirect":
			s.SetValue("followRedirects", false)
			has = true
		case "timeout":
			if ms, assumed, ok := parseTimeout(v); ok {
				s.SetValue("timeoutMs", ms)
				has = true
				if assumed {
					c.add(impkit.Degraded, itemPath, it.UID, "timeout-unit-assumed", "@timeout "+v+" has no unit; milliseconds were assumed", "Check the value")
				}
			} else {
				c.add(impkit.Degraded, itemPath, it.UID, "directive:timeout", "@timeout value "+strconv.Quote(v)+" could not be read and was ignored", "")
			}
		case "prompt":
			c.add(impkit.NeedsReview, itemPath, it.UID, "directive:prompt", "@prompt "+v+" asks for a value at run time; the variable stays unresolved and blocks the send", "Define the variable")
		default:
			if m, ok := settingsMsgs[k]; ok {
				c.add(impkit.Degraded, itemPath, it.UID, "directive:"+k, m, "")
			} else {
				c.add(impkit.Degraded, itemPath, it.UID, "directive:"+k, "unknown directive @"+k+" ignored", "")
			}
		}
	}
	if has {
		it.Settings = impkit.MustJSON(s)
	}
}

var timeoutRe = regexp.MustCompile(`^(\d+)\s*(ms|s|m)?$`)

func parseTimeout(v string) (ms int64, assumed, ok bool) {
	m := timeoutRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(v)))
	if m == nil {
		return 0, false, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n > 1<<40 {
		return 0, false, false
	}
	switch m[2] {
	case "s":
		return n * 1000, false, true
	case "m":
		return n * 60000, false, true
	case "ms":
		return n, false, true
	}
	return n, true, true
}

// recordScript records an inert, quarantined script. impkit.RecordScript only
// recognises pm./bru./req./res./insomnia. APIs, so a JetBrains script using
// client.* / response.* would be graded "supported"; there is no shim for this
// dialect, so every script here is forced to unsupported.
func (c *conv) recordScript(itemPath, uid string, s script) {
	rep := &c.res.Report
	before := len(rep.ScriptList)
	impkit.RecordScript(rep, itemPath, uid, s.listen, "jetbrains", s.src)
	if len(rep.ScriptList) == before {
		return
	}
	sl := &rep.ScriptList[len(rep.ScriptList)-1]
	if sl.Status != "unsupported" {
		switch sl.Status {
		case "supported":
			rep.Scripts.Supported--
		case "partial":
			rep.Scripts.Partial--
		}
		rep.Scripts.Unsupported++
		sl.Status = "unsupported"
		for i := len(rep.Entries) - 1; i >= 0; i-- {
			if rep.Entries[i].Feature == "script-quarantined" {
				rep.Entries[i].Message = strings.Replace(rep.Entries[i].Message, ", supported)", ", unsupported)", 1)
				rep.Entries[i].Message = strings.Replace(rep.Entries[i].Message, ", partial)", ", unsupported)", 1)
				break
			}
		}
		c.add(impkit.Unsupported, itemPath, uid, "script-api:jetbrains", "jetbrains HTTP-client script API (client.*, response.*) has no shim in this build; the script reports 'unsupported' at run time instead of a false pass", "")
	}
	c.add(impkit.PreservedInert, itemPath, uid, "script-inert", fmt.Sprintf("%s script kept as an inert event (dialect jetbrains); never executed or translated", s.listen), "")
}

// ---- body ----

var (
	boundaryRe = regexp.MustCompile(`(?i)boundary\s*=\s*"?([^";\s]+)"?`)
	dispNameRe = regexp.MustCompile(`(?i)\bname\s*=\s*"([^"]*)"`)
	dispFileRe = regexp.MustCompile(`(?i)\bfilename\s*=\s*"([^"]*)"`)
)

func headerOf(rows []impkit.Row, name string) string {
	for _, h := range rows {
		if strings.EqualFold(h.Key, name) {
			return h.Value
		}
	}
	return ""
}

func (c *conv) body(r *req, it *store.Item, itemPath string) {
	if len(r.body) == 0 {
		return
	}
	text := strings.Join(r.body, "\n")
	if len(text) > MaxBodyBytes {
		text = impkit.Clip(text, MaxBodyBytes)
		r.note(impkit.Degraded, "body-truncated", "body larger than 4 MiB was truncated", "")
	}
	ct := headerOf(r.headers, "Content-Type")
	// single-file body
	if inc, ok := singleInclude(r.body); ok {
		c.res.Report.Stats.NeedsAsset++
		c.add(impkit.NeedsReview, itemPath, it.UID, "needs-asset", "body is read from file "+inc+"; files are never read at import", "Attach the file to the request body")
		it.Body = impkit.MustJSON(map[string]any{"mode": "file", "file": map[string]string{"src": ""}})
		return
	}
	if strings.EqualFold(strings.TrimSpace(headerOf(r.headers, "X-REQUEST-TYPE")), "graphql") {
		c.graphql(r, it, itemPath, text)
		return
	}
	if strings.HasPrefix(strings.ToLower(ct), "multipart/") {
		if m := boundaryRe.FindStringSubmatch(ct); m != nil {
			if rows, ok := c.multipart(text, m[1], it, itemPath); ok {
				it.Body = impkit.MustJSON(map[string]any{"mode": "formdata", "formdata": rows})
				return
			}
		}
		c.add(impkit.Degraded, itemPath, it.UID, "multipart-raw", "multipart body could not be split into parts; kept as raw text", "")
	}
	for _, ln := range r.body {
		if m := bodyIncludeRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil && !strings.HasPrefix(strings.TrimSpace(ln), "<{") {
			c.res.Report.Stats.NeedsAsset++
			c.add(impkit.NeedsReview, itemPath, it.UID, "needs-asset", "body line includes file "+strings.TrimSpace(m[2])+"; the include stays literal and the file is never read", "Paste the content or attach the file")
		}
	}
	text = c.rewrite(text, itemPath, it.UID, true)
	it.Body = impkit.RawBody(text, impkit.LanguageFor(ct))
}

func singleInclude(lines []string) (string, bool) {
	var only string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if only != "" {
			return "", false
		}
		m := bodyIncludeRe.FindStringSubmatch(t)
		if m == nil || strings.HasPrefix(strings.TrimSpace(m[2]), "{%") {
			return "", false
		}
		only = strings.TrimSpace(m[2])
	}
	return only, only != ""
}

func (c *conv) graphql(r *req, it *store.Item, itemPath, text string) {
	query, vars := text, ""
	if i := strings.Index(text, "\n\n"); i >= 0 {
		query, vars = text[:i], strings.TrimSpace(text[i+2:])
	}
	query = c.rewrite(query, itemPath, it.UID, true)
	gv := map[string]any{"query": query}
	if vars != "" {
		vars = c.rewrite(vars, itemPath, it.UID, true)
		if json.Valid([]byte(vars)) {
			gv["variables"] = json.RawMessage(vars)
		} else {
			gv["variables"] = vars
			c.add(impkit.Degraded, itemPath, it.UID, "body.graphql", "GraphQL variables were not valid JSON; kept as text", "")
		}
	}
	// the marker header is consumed by the graphql body mode
	var kept []impkit.Row
	for _, h := range r.headers {
		if !strings.EqualFold(h.Key, "X-REQUEST-TYPE") {
			kept = append(kept, h)
		}
	}
	r.headers = kept
	it.Body = impkit.MustJSON(map[string]any{"mode": "graphql", "graphql": gv})
	c.add(impkit.Converted, itemPath, it.UID, "body.graphql", "GraphQL query and variables preserved (X-REQUEST-TYPE header consumed)", "")
}

// multipart splits a body on its boundary into formdata rows.
func (c *conv) multipart(text, boundary string, it *store.Item, itemPath string) ([]map[string]any, bool) {
	delim := "--" + boundary
	var rows []map[string]any
	var cur []string
	in, done := false, false
	flush := func() {
		if in && len(cur) > 0 {
			if row, ok := c.part(cur, it, itemPath); ok {
				rows = append(rows, row)
			}
		}
		cur = nil
	}
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimRight(ln, " \t")
		if t == delim+"--" {
			flush()
			done = true
			break
		}
		if t == delim {
			flush()
			in = true
			continue
		}
		if in {
			cur = append(cur, ln)
		}
	}
	if !done {
		flush()
	}
	return rows, len(rows) > 0
}

func (c *conv) part(lines []string, it *store.Item, itemPath string) (map[string]any, bool) {
	var hdrs []impkit.Row
	i := 0
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			i++
			break
		}
		if m := headerRe.FindStringSubmatch(t); m != nil {
			hdrs = append(hdrs, impkit.Row{Key: m[1], Value: strings.TrimSpace(m[2])})
		}
	}
	content := lines[min(i, len(lines)):]
	for len(content) > 0 && strings.TrimSpace(content[len(content)-1]) == "" {
		content = content[:len(content)-1]
	}
	disp := headerOf(hdrs, "Content-Disposition")
	nm := dispNameRe.FindStringSubmatch(disp)
	if nm == nil {
		return nil, false
	}
	row := map[string]any{"key": nm[1]}
	if pct := headerOf(hdrs, "Content-Type"); pct != "" {
		row["contentType"] = pct
	}
	fn := dispFileRe.FindStringSubmatch(disp)
	if inc, ok := singleInclude(content); ok {
		row["type"], row["src"] = "file", ""
		if fn != nil {
			row["fileName"] = fn[1]
		}
		c.res.Report.Stats.NeedsAsset++
		c.add(impkit.NeedsReview, itemPath, it.UID, "needs-asset", "form field "+nm[1]+" uploads file "+inc+"; files are never read at import", "Attach the file in the form editor")
		return row, true
	}
	row["type"] = "text"
	row["value"] = c.rewrite(strings.Join(content, "\n"), itemPath, it.UID, true)
	if fn != nil {
		row["fileName"] = fn[1]
		c.add(impkit.Degraded, itemPath, it.UID, "multipart-inline-file", "form field "+nm[1]+" has a filename with inline content; imported as a text value", "")
	}
	return row, true
}

// ---- templates ----

var (
	chainRe  = regexp.MustCompile(`^[A-Za-z_][\w-]*\.(?:response|request)\.`)
	dynArgRe = regexp.MustCompile(`^\$([A-Za-z][\w.]*)(.*)$`)
)

// jbDynamic maps JetBrains / VS Code dynamic variables with no arguments to
// the Postman dynamic variables the resolver evaluates.
var dynMap = map[string]string{
	"uuid": "$guid", "guid": "$guid", "random.uuid": "$guid",
	"timestamp": "$timestamp", "isoTimestamp": "$isoTimestamp",
	"randomInt": "$randomInt", "random.integer": "$randomInt",
	"random.email": "$randomEmail", "random.alphanumeric": "",
}

// rewrite maps dynamic variables, records plain references and flags
// request chaining. It never resolves anything. When report is true it adds
// report entries and tracks references for the undefined-variable pass.
func (c *conv) rewrite(s, path, uid string, report bool) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return tmplRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.TrimSpace(m[2 : len(m)-2])
		switch {
		case strings.HasPrefix(inner, "$"):
			return c.dynamic(m, inner, path, uid, report)
		case chainRe.MatchString(inner):
			if report {
				c.add(impkit.NeedsReview, path, uid, "request-chaining", "{{"+inner+"}} refers to another request's response and cannot be resolved at import; it stays literal and blocks the send until a variable of that name is set", "Extract the value with a script or set the variable by hand")
			}
		default:
			if report && !strings.ContainsAny(inner, " \t") {
				if _, ok := c.refs[inner]; !ok {
					c.refs[inner] = path
					c.order = append(c.order, inner)
				}
			}
		}
		return m
	})
}

func (c *conv) dynamic(orig, inner, path, uid string, report bool) string {
	m := dynArgRe.FindStringSubmatch(inner)
	name, args := "", ""
	if m != nil {
		name, args = m[1], strings.TrimSpace(m[2])
	}
	// $random.integer(1, 10) and friends: arguments are not representable
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
		args = "(...)"
	}
	if k := strings.IndexByte(inner, '('); k >= 0 && args == "" {
		args = inner[k:]
		name = strings.TrimPrefix(inner[:k], "$")
	}
	to, known := dynMap[name]
	switch {
	case name == "datetime" && strings.EqualFold(args, "iso8601"):
		return "{{$isoTimestamp}}"
	case known && to != "" && args == "":
		return "{{" + to + "}}"
	}
	if report {
		why := "has no equivalent built-in dynamic variable"
		switch {
		case known && args != "":
			why = "uses arguments or offsets that the built-in " + to + " does not support"
		case name == "processEnv" || name == "dotenv" || name == "aadToken" || strings.HasPrefix(name, "env"):
			why = "reads the process environment or a secret store, which is never done at import"
		}
		c.add(impkit.NeedsReview, path, uid, "dynamic-variable:$"+name, "{{"+inner+"}} "+why+"; it stays literal and blocks the send", "Replace it with a defined variable")
	}
	return orig
}

// undefined reports plain {{name}} references that the file does not define:
// they usually come from http-client.env.json / settings.json environments,
// which are reported, never resolved.
func (c *conv) undefined() {
	for _, n := range c.order {
		if _, ok := c.defined[n]; ok {
			continue
		}
		c.add(impkit.NeedsReview, c.refs[n], "", "environment-variable", "{{"+n+"}} is not defined in this file; it most likely comes from http-client.env.json / settings.json (or a prompt), which are not read", "Create an Interseptor environment variable named "+n)
	}
}
