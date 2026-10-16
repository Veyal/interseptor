package bruno

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

var verbs = map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true, "options": true, "head": true, "connect": true, "trace": true}

var knownBlocks = map[string]bool{
	"meta": true, "params:query": true, "params:path": true, "headers": true, "assert": true, "tests": true, "docs": true,
	"vars:pre-request": true, "vars:post-response": true, "script:pre-request": true, "script:post-response": true,
	"body:json": true, "body:text": true, "body:xml": true, "body:sparql": true, "body:graphql": true, "body:graphql:vars": true,
	"body:form-urlencoded": true, "body:multipart-form": true, "body:file": true, "settings": true, "vars": true, "vars:secret": true,
}

type blkCtx struct {
	c    *conv
	path string
	uid  string
}

func (x *blkCtx) add(l impkit.Level, feature, msg, sugg string) {
	x.c.add(l, x.path, x.uid, feature, msg, sugg)
}

func (x *blkCtx) rows(b *block) []impkit.Row {
	out := make([]impkit.Row, 0, len(b.Pairs))
	for _, p := range b.Pairs {
		if p.Disabled {
			x.c.res.Report.Stats.DisabledRows++
		}
		out = append(out, impkit.Row{Key: p.Key, Value: p.Value, Disabled: p.Disabled})
	}
	return out
}

func (x *blkCtx) unknownBlocks(b *bru) {
	for _, bl := range b.blocks {
		if knownBlocks[bl.Name] || verbs[bl.Name] || strings.HasPrefix(bl.Name, "auth") {
			continue
		}
		if bl.Name == "example" || strings.HasPrefix(bl.Name, "example") {
			x.add(impkit.Degraded, "examples", "saved response examples are not imported", "")
			continue
		}
		x.add(impkit.Degraded, "unknown-block:"+bl.Name, "block "+bl.Name+" is not recognized and was ignored", "")
	}
}

// vars maps vars:pre-request to owner variables.
func (x *blkCtx) vars(b *bru, owner, uid string) {
	if v := b.get("vars:pre-request"); v != nil {
		for _, p := range v.Pairs {
			sv := store.Variable{OwnerKind: owner, OwnerUID: uid, Key: p.Key, Type: store.VarTypeDefault, InitialValue: p.Value, Enabled: !p.Disabled}
			if impkit.IsSecretKey(p.Key) && p.Value != "" && !strings.Contains(p.Value, "{{") {
				sv.Type, sv.InitialValue = store.VarTypeSecret, ""
				x.c.res.SecretValues = append(x.c.res.SecretValues, postman.SecretValue{OwnerKind: owner, OwnerUID: uid, Key: p.Key, Value: p.Value})
				x.c.res.Report.Stats.SecretVariables++
				x.add(impkit.NeedsReview, "secret-variable", "variable "+p.Key+" holds a secret value; it was moved out of the shareable initial value", "")
			}
			x.c.res.Report.Stats.Variables++
			x.c.res.Variables = append(x.c.res.Variables, sv)
		}
	}
}

func (x *blkCtx) auth(b *bru) json.RawMessage {
	mode := ""
	for _, bl := range b.blocks {
		if verbs[bl.Name] || bl.Name == "auth" {
			if m := pair(&bl, "auth"); m != "" {
				mode = m
			}
		}
	}
	if mode == "" {
		// folder/collection style: auth { mode: x }
		if bl := b.get("auth"); bl != nil {
			mode = pair(bl, "mode")
		}
	}
	switch mode {
	case "", "inherit":
		return nil
	case "none":
		return impkit.AuthObj("noauth", nil)
	}
	blk := b.get("auth:" + mode)
	get := func(k string) string { return pair(blk, k) }
	cred := func(what, v string) {
		if v != "" && !strings.Contains(v, "{{") {
			impkit.CountCredential(x.c.rep(), x.path, x.uid, "auth "+mode+"."+what)
		}
	}
	switch mode {
	case "bearer":
		cred("token", get("token"))
		return impkit.AuthObj("bearer", [][2]string{{"token", get("token")}})
	case "basic", "digest", "ntlm":
		cred("password", get("password"))
		if mode == "ntlm" { // digest is applied by the send pipeline; ntlm has no authenticator
			x.add(impkit.PreservedInert, "auth:"+mode, "auth type "+mode+" is kept but has no authenticator: the request is sent without it", "Add the header manually")
		}
		return impkit.AuthObj(mode, [][2]string{{"username", get("username")}, {"password", get("password")}})
	case "apikey":
		in := "header"
		if strings.Contains(strings.ToLower(get("placement")), "query") {
			in = "query"
		}
		cred("value", get("value"))
		return impkit.AuthObj("apikey", [][2]string{{"key", get("key")}, {"value", get("value")}, {"in", in}})
	}
	var kv [][2]string
	if blk != nil {
		for _, p := range blk.Pairs {
			if impkit.IsSecretKey(p.Key) {
				cred(p.Key, p.Value)
			}
			kv = append(kv, [2]string{p.Key, p.Value})
		}
	}
	x.add(impkit.PreservedInert, "auth:"+mode, "auth type "+mode+" is kept but not applied when sending", "Obtain a token manually and use bearer auth")
	return impkit.AuthObj(mode, kv)
}

// scripts turns script:*/tests blocks into quarantined events.
func (x *blkCtx) scripts(b *bru, record bool) json.RawMessage {
	var evs []json.RawMessage
	add := func(blockName, listen string) {
		bl := b.get(blockName)
		if bl == nil || strings.TrimSpace(bl.Text) == "" {
			return
		}
		evs = append(evs, impkit.ScriptEvent(listen, "bruno", bl.Text))
		if record {
			impkit.RecordScript(x.c.rep(), x.path, x.uid, listen, "bruno", bl.Text)
		}
	}
	add("script:pre-request", "prerequest")
	add("script:post-response", "test")
	add("tests", "test")
	return impkit.Events(evs)
}

func (c *conv) request(ch child, it *store.Item, p string, inherit []impkit.Row) {
	b := ch.req
	x := &blkCtx{c: c, path: p, uid: it.UID}
	method, verb := "GET", (*block)(nil)
	for i := range b.blocks {
		if verbs[b.blocks[i].Name] {
			method, verb = strings.ToUpper(b.blocks[i].Name), &b.blocks[i]
			break
		}
	}
	if verb == nil {
		x.add(impkit.Unsupported, "no-http-verb", "no get/post/... block found; the request has no URL", "")
	}
	if m := b.get("meta"); m != nil {
		if t := pair(m, "type"); t != "" && t != "http" && t != "graphql" {
			x.add(impkit.Unsupported, "request-type:"+t, "request type "+t+" is not supported (HTTP and GraphQL only)", "")
		}
	}
	it.Method = method
	raw := pair(verb, "url")
	it.URL = x.url(b, raw)
	headers := append([]impkit.Row(nil), inherit...)
	if h := b.get("headers"); h != nil {
		headers = append(headers, x.rows(h)...)
	}
	it.Body, headers = x.body(b, verb, headers)
	impkit.ScanHeaderCredentials(c.rep(), p, it.UID, headers)
	it.Headers = impkit.Rows(headers)
	it.Auth = x.auth(b)
	if d := b.get("docs"); d != nil {
		it.DescriptionMD = d.Text
	}
	it.Events = x.scripts(b, true)
	it.Assertions = x.asserts(b)
	if v := b.get("vars:post-response"); v != nil && len(v.Pairs) > 0 {
		var ex []map[string]any
		for _, kv := range v.Pairs {
			ex = append(ex, map[string]any{"name": kv.Key, "expr": kv.Value, "disabled": kv.Disabled})
		}
		it.Vars = impkit.MustJSON(map[string]any{"extract": ex})
		x.add(impkit.PreservedInert, "vars:post-response", "post-response variable expressions are stored but evaluated only by trusted scripts", "Use a post-response script or an extractor")
	}
	if s := b.get("settings"); s != nil && len(s.Pairs) > 0 {
		om := postman.NewOMap()
		for _, kv := range s.Pairs {
			om.SetValue("bruno."+kv.Key, kv.Value)
		}
		it.Settings = impkit.MustJSON(om)
	}
	x.vars(b, store.VarOwnerRequest, it.UID)
	x.unknownBlocks(b)
	if u := raw; strings.Contains(u, "@") && !strings.Contains(u, "{{") {
		hp := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/", 2)[0]
		if strings.Contains(hp, ":") && strings.Contains(hp, "@") {
			impkit.CountCredential(c.rep(), p, it.UID, "URL userinfo")
		}
	}
}

// url builds the url object: raw URL, path params and disabled query rows.
func (x *blkCtx) url(b *bru, raw string) json.RawMessage {
	var pvars []impkit.PathVar
	if pp := b.get("params:path"); pp != nil {
		for _, kv := range pp.Pairs {
			pvars = append(pvars, impkit.PathVar{Key: kv.Key, Value: kv.Value})
		}
	}
	var extra, enabled []impkit.Row
	if pq := b.get("params:query"); pq != nil {
		for _, kv := range pq.Pairs {
			r := impkit.Row{Key: kv.Key, Value: kv.Value, Disabled: kv.Disabled}
			if kv.Disabled {
				extra = append(extra, r)
				x.c.res.Report.Stats.DisabledRows++
			} else {
				enabled = append(enabled, r)
			}
		}
	}
	if !strings.Contains(raw, "?") && len(enabled) > 0 {
		raw = impkit.AppendQuery(raw, enabled)
	}
	return impkit.URLObjectWith(raw, pvars, extra)
}

func (x *blkCtx) body(b *bru, verb *block, headers []impkit.Row) (json.RawMessage, []impkit.Row) {
	mode := pair(verb, "body")
	has := func(n string) bool {
		for _, h := range headers {
			if !h.Disabled && strings.EqualFold(h.Key, n) {
				return true
			}
		}
		return false
	}
	ct := func(v string) {
		if !has("Content-Type") {
			headers = append(headers, impkit.Row{Key: "Content-Type", Value: v})
		}
	}
	text := func(name string) string {
		if bl := b.get(name); bl != nil {
			return bl.Text
		}
		return ""
	}
	switch mode {
	case "", "none":
		return nil, headers
	case "json":
		ct("application/json")
		return impkit.RawBody(text("body:json"), "json"), headers
	case "text":
		ct("text/plain")
		return impkit.RawBody(text("body:text"), "text"), headers
	case "xml":
		ct("application/xml")
		return impkit.RawBody(text("body:xml"), "xml"), headers
	case "sparql":
		ct("application/sparql-query")
		return impkit.RawBody(text("body:sparql"), "text"), headers
	case "graphql":
		g := map[string]any{"query": text("body:graphql")}
		if v := strings.TrimSpace(text("body:graphql:vars")); v != "" {
			var j any
			if json.Unmarshal([]byte(v), &j) == nil {
				g["variables"] = j
			} else {
				g["variables"] = v
			}
		}
		ct("application/json")
		return impkit.MustJSON(map[string]any{"mode": "graphql", "graphql": g}), headers
	case "formUrlEncoded":
		bl := b.get("body:form-urlencoded")
		out := []map[string]any{}
		if bl != nil {
			for _, kv := range bl.Pairs {
				row := map[string]any{"key": kv.Key, "value": kv.Value}
				if kv.Disabled {
					row["disabled"] = true
					x.c.res.Report.Stats.DisabledRows++
				}
				out = append(out, row)
			}
		}
		ct("application/x-www-form-urlencoded")
		return impkit.MustJSON(map[string]any{"mode": "urlencoded", "urlencoded": out}), headers
	case "multipartForm":
		bl := b.get("body:multipart-form")
		out := []map[string]any{}
		if bl != nil {
			for _, kv := range bl.Pairs {
				row := map[string]any{"key": kv.Key}
				if strings.HasPrefix(kv.Value, "@file(") {
					row["type"], row["src"] = "file", ""
					x.c.res.Report.Stats.NeedsAsset++
					x.add(impkit.NeedsReview, "needs-asset", "form field "+kv.Key+" uploads a local file; files are never read at import", "Attach the file in the form editor")
				} else {
					row["type"], row["value"] = "text", kv.Value
				}
				if kv.Disabled {
					row["disabled"] = true
					x.c.res.Report.Stats.DisabledRows++
				}
				out = append(out, row)
			}
		}
		return impkit.MustJSON(map[string]any{"mode": "formdata", "formdata": out}), headers
	case "file":
		x.c.res.Report.Stats.NeedsAsset++
		x.add(impkit.NeedsReview, "needs-asset", "file body references a local file; files are never read at import", "Attach the file to the request")
		return impkit.MustJSON(map[string]any{"mode": "file", "file": map[string]string{"src": ""}}), headers
	}
	x.add(impkit.Unsupported, "body.mode:"+mode, "body mode "+mode+" is not supported and was not imported", "")
	return nil, headers
}

// ---- assertions ----

var numRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

var assertOps = map[string]string{"eq": "eq", "neq": "ne", "gt": "gt", "gte": "gte", "lt": "lt", "lte": "lte", "in": "in",
	"contains": "contains", "notContains": "notcontains", "matches": "regex", "isDefined": "exists", "isUndefined": "notexists"}

type assertion struct {
	ID       string          `json:"id"`
	Name     string          `json:"name,omitempty"`
	Type     string          `json:"type"`
	Op       string          `json:"op,omitempty"`
	Header   string          `json:"header,omitempty"`
	Path     string          `json:"path,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Disabled bool            `json:"disabled,omitempty"`
}

// asserts converts Bruno assert rows into declarative assertions. Operators
// or targets without an equivalent become type "unsupported" entries, which
// the runner reports as unsupported, never as a pass.
func (x *blkCtx) asserts(b *bru) json.RawMessage {
	bl := b.get("assert")
	if bl == nil || len(bl.Pairs) == 0 {
		return nil
	}
	var out []assertion
	for i, kv := range bl.Pairs {
		a := assertion{ID: "a" + strconv.Itoa(i+1), Name: strings.TrimSpace(kv.Key + ": " + kv.Value), Disabled: kv.Disabled}
		opTok, rest, _ := strings.Cut(strings.TrimSpace(kv.Value), " ")
		op, okOp := assertOps[opTok]
		rest = strings.TrimSpace(rest)
		okTarget := x.target(&a, kv.Key)
		if !okOp || !okTarget {
			a.Type, a.Op, a.Path, a.Header = "unsupported", "", "", ""
			x.add(impkit.Unsupported, "assert:"+opTok, "assertion '"+kv.Key+": "+opTok+"' has no equivalent; the runner reports it unsupported instead of passing", "Rewrite it as a script or a supported operator")
		} else {
			a.Op = op
			if op != "exists" && op != "notexists" {
				a.Value = assertValue(rest)
			}
			x.c.res.Report.Entries = append(x.c.res.Report.Entries, impkit.Entry{Level: impkit.Converted, Path: x.path, Item: x.uid, Feature: "assertion", Message: "assertion " + a.Name + " converted"})
		}
		out = append(out, a)
	}
	return impkit.MustJSON(out)
}

func (x *blkCtx) target(a *assertion, lhs string) bool {
	lhs = strings.TrimSpace(lhs)
	switch {
	case lhs == "res.status":
		a.Type = "status"
	case lhs == "res.responseTime":
		a.Type = "time"
	case lhs == "res.body":
		a.Type = "body"
	case strings.HasPrefix(lhs, "res.headers."):
		a.Type, a.Header = "header", lhs[len("res.headers."):]
	case strings.HasPrefix(lhs, "res.body."):
		rest := lhs[len("res.body."):]
		if strings.ContainsAny(rest, "()") || strings.HasSuffix(rest, ".length") || rest == "length" {
			return false
		}
		a.Type, a.Path = "jsonpath", "$."+rest
	case strings.HasPrefix(lhs, "res.body["):
		a.Type, a.Path = "jsonpath", "$"+lhs[len("res.body"):]
	default:
		return false
	}
	return true
}

func assertValue(s string) json.RawMessage {
	switch {
	case s == "true" || s == "false" || s == "null" || numRe.MatchString(s):
		return json.RawMessage(s)
	case len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\''):
		return impkit.MustJSON(s[1 : len(s)-1])
	case strings.HasPrefix(s, "["):
		var j any
		if json.Unmarshal([]byte(s), &j) == nil {
			return json.RawMessage(s)
		}
	}
	return impkit.MustJSON(s)
}
