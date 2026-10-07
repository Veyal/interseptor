package insomnia

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

type builder struct {
	res    *Result
	opt    Options
	ids    map[string]string // insomnia id -> uid
	chains int
}

// assign gives every node its uid up front so response-chaining tags can
// reference requests that appear later in the tree.
func (b *builder) assign(ns []*node, depth int) {
	for _, n := range ns {
		uid := b.opt.NewID()
		if n.id != "" {
			b.ids[n.id] = uid
		}
		n.id2 = uid
		if depth < impkit.MaxDepth {
			b.assign(n.children, depth+1)
		}
	}
}

func (b *builder) rep() *impkit.Report { return &b.res.Report }

func (b *builder) add(l impkit.Level, path, uid, feature, msg, sugg string) {
	b.rep().Entries = append(b.rep().Entries, impkit.Entry{Level: l, Path: path, Item: uid, Feature: feature, Message: msg, Suggestion: sugg})
}

// emit writes a node (and its children) into the result and returns its rank.
func (b *builder) emit(n *node, parent, prevRank, parentPath string, depth int, inherit []impkit.Row) string {
	if len(b.res.Items) >= MaxItems {
		return prevRank
	}
	if depth >= impkit.MaxDepth {
		b.add(impkit.Blocked, parentPath, "", "too-deep", "folder nesting deeper than 64 levels was not imported", "")
		return prevRank
	}
	name := n.name
	if strings.TrimSpace(name) == "" {
		name = "Untitled"
	}
	path := parentPath + "/" + name
	it := store.Item{UID: n.id2, CollectionUID: b.res.Collection.UID, ParentUID: parent, Rank: store.RankBetween(prevRank, ""),
		Name: name, DescriptionMD: n.desc}
	side := postman.ItemSidecar{ID: n.id}
	if n.folder {
		it.Kind = "folder"
		b.res.Report.Stats.Folders++
		inherit = b.folder(n, &it, path, inherit)
	} else {
		it.Kind = "request"
		b.res.Report.Stats.Requests++
		b.request(n, &it, &side, path, inherit)
	}
	it.Sidecar = impkit.MustJSON(side)
	b.res.Items = append(b.res.Items, it)
	prev := ""
	for _, c := range sortNodes(n.children) {
		prev = b.emit(c, it.UID, prev, path, depth+1, inherit)
	}
	return it.Rank
}

// folder applies folder-level settings and returns the headers children
// inherit (the execution pipeline only applies request headers, so folder
// headers are copied into each request below).
func (b *builder) folder(n *node, it *store.Item, path string, inherit []impkit.Row) []impkit.Row {
	ctx := &tctx{b: b, path: path, uid: it.UID}
	out := inherit
	if len(n.headers) > 0 {
		hs := make([]impkit.Row, len(n.headers))
		for i, h := range n.headers {
			hs[i] = impkit.Row{Key: ctx.conv(h.Key), Value: ctx.conv(h.Value), Disabled: h.Disabled}
		}
		impkit.ScanHeaderCredentials(b.rep(), path, it.UID, hs)
		b.add(impkit.Degraded, path, it.UID, "inherited-headers", "folder headers are copied into each request below because folders do not apply headers when sending", "")
		out = append(append([]impkit.Row(nil), inherit...), hs...)
	}
	it.Auth = b.auth(n.auth, ctx)
	for _, k := range sortedKeys(n.env) {
		b.variable(store.VarOwnerFolder, it.UID, k, n.env[k], path)
	}
	ctx.finish()
	return out
}

func (b *builder) variable(kind, owner, key string, val any, path string) {
	flat := map[string]string{}
	flatten(key, val, flat, 0)
	for _, k := range sortedKeys(flat) {
		v := store.Variable{OwnerKind: kind, OwnerUID: owner, Key: k, Type: store.VarTypeDefault, Enabled: true}
		raw := b.convPlain(flat[k])
		if impkit.IsSecretKey(k) && raw != "" {
			v.Type = store.VarTypeSecret
			b.res.Report.Stats.SecretVariables++
			if !strings.Contains(raw, "{{") {
				b.res.SecretValues = append(b.res.SecretValues, postman.SecretValue{OwnerKind: kind, OwnerUID: owner, Key: k, Value: raw})
				b.add(impkit.NeedsReview, path, "", "secret-variable", "variable "+k+" looks like a secret; its value was moved out of the shareable initial value (offered as a local current value only)", "")
			} else {
				v.InitialValue = raw
			}
		} else {
			v.InitialValue = raw
		}
		b.res.Report.Stats.Variables++
		b.res.Variables = append(b.res.Variables, v)
	}
}

// flatten turns nested environment data into dotted keys (Insomnia reaches
// into objects with {{ _.a.b }}); arrays and scalars become strings.
func flatten(prefix string, v any, out map[string]string, depth int) {
	if len(out) > 20000 {
		return
	}
	if m, ok := v.(map[string]any); ok && depth < 16 {
		if len(m) == 0 {
			out[prefix] = ""
		}
		for k, x := range m {
			flatten(prefix+"."+k, x, out, depth+1)
		}
		return
	}
	switch t := v.(type) {
	case nil:
		out[prefix] = ""
	case string:
		out[prefix] = t
	case json.Number:
		out[prefix] = t.String()
	case bool, int, int64, float64:
		out[prefix] = fmt.Sprint(t)
	default:
		bs, _ := postman.Marshal(t)
		out[prefix] = string(bs)
	}
}

func (b *builder) request(n *node, it *store.Item, side *postman.ItemSidecar, path string, inherit []impkit.Row) {
	ctx := &tctx{b: b, path: path, uid: it.UID}
	it.Method = strings.ToUpper(strings.TrimSpace(n.method))
	if it.Method == "" {
		it.Method = "GET"
	}
	raw := ctx.conv(n.url)
	var pvars []impkit.PathVar
	if n.params != nil {
		q := make([]impkit.Row, len(n.params))
		for i, p := range n.params {
			q[i] = impkit.Row{Key: ctx.conv(p.Key), Value: ctx.conv(p.Value), Disabled: p.Disabled}
		}
		raw = impkit.AppendQuery(raw, q)
		for _, p := range q {
			if p.Disabled {
				b.res.Report.Stats.DisabledRows++
			}
		}
	}
	it.URL = impkit.URLObject(raw, pvars)
	hs := append(append([]impkit.Row(nil), inherit...), b.headers(n, ctx)...)
	it.Headers = impkit.Rows(hs)
	impkit.ScanHeaderCredentials(b.rep(), path, it.UID, hs)
	it.Body = b.body(n, it, ctx)
	it.Auth = b.auth(n.auth, ctx)
	it.Settings = b.settings(n.settings, path, it.UID)
	b.scripts(n, it, path)
	if u := raw; strings.Contains(u, "@") {
		if hp := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/", 2)[0]; strings.Contains(hp, ":") && strings.Contains(hp, "@") && !strings.Contains(hp, "{{") {
			impkit.CountCredential(b.rep(), path, it.UID, "URL userinfo")
		}
	}
	ctx.finish()
	if len(ctx.chain) > 0 {
		it.Vars = impkit.MustJSON(map[string]any{"extract": ctx.chain})
	}
	_ = side
}

func (b *builder) headers(n *node, ctx *tctx) []impkit.Row {
	out := make([]impkit.Row, 0, len(n.headers)+1)
	has := false
	for _, h := range n.headers {
		r := impkit.Row{Key: ctx.conv(h.Key), Value: ctx.conv(h.Value), Disabled: h.Disabled}
		if strings.EqualFold(r.Key, "content-type") && !r.Disabled {
			has = true
		}
		if r.Disabled {
			b.res.Report.Stats.DisabledRows++
		}
		out = append(out, r)
	}
	if mt := strings.TrimSpace(str(n.body, "mimeType")); mt != "" && !has && !strings.HasPrefix(mt, "multipart/") &&
		(str(n.body, "text") != "" || len(arr(n.body, "params")) > 0) {
		if mt == "application/graphql" {
			mt = "application/json" // the body is sent as a JSON {query,variables} envelope
		}
		out = append(out, impkit.Row{Key: "Content-Type", Value: mt})
	}
	return out
}

func (b *builder) body(n *node, it *store.Item, ctx *tctx) json.RawMessage {
	if len(n.body) == 0 {
		return nil
	}
	mt := strings.ToLower(strings.TrimSpace(str(n.body, "mimeType")))
	text := ctx.conv(str(n.body, "text"))
	params := rows(arr(n.body, "params"))
	switch {
	case mt == "application/x-www-form-urlencoded":
		out := make([]map[string]any, 0, len(params))
		for _, p := range params {
			row := map[string]any{"key": ctx.conv(p.Key), "value": ctx.conv(p.Value)}
			if p.Disabled {
				row["disabled"] = true
				b.res.Report.Stats.DisabledRows++
			}
			out = append(out, row)
		}
		return impkit.MustJSON(map[string]any{"mode": "urlencoded", "urlencoded": out})
	case strings.HasPrefix(mt, "multipart/"):
		out := make([]map[string]any, 0, len(params))
		for _, p := range params {
			row := map[string]any{"key": ctx.conv(p.Key)}
			if p.Type == "file" {
				row["type"], row["src"] = "file", ""
				if p.Value != "" {
					row["fileName"] = p.Value
				}
				b.res.Report.Stats.NeedsAsset++
				b.add(impkit.NeedsReview, ctx.path, it.UID, "needs-asset", "form field "+p.Key+" uploads a local file; files are never read at import", "Attach the file in the form editor")
			} else {
				row["type"], row["value"] = "text", ctx.conv(p.Value)
			}
			if p.Disabled {
				row["disabled"] = true
				b.res.Report.Stats.DisabledRows++
			}
			out = append(out, row)
		}
		return impkit.MustJSON(map[string]any{"mode": "formdata", "formdata": out})
	case mt == "application/graphql":
		var g struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		if json.Unmarshal([]byte(text), &g) == nil && g.Query != "" {
			gv := map[string]any{"query": g.Query}
			if g.Variables != nil {
				gv["variables"] = g.Variables
			}
			return impkit.MustJSON(map[string]any{"mode": "graphql", "graphql": gv})
		}
		b.add(impkit.Degraded, ctx.path, it.UID, "body.graphql", "GraphQL body was not valid JSON; kept as raw text", "")
		return impkit.RawBody(text, "json")
	case str(n.body, "fileName") != "":
		b.res.Report.Stats.NeedsAsset++
		b.add(impkit.NeedsReview, ctx.path, it.UID, "needs-asset", "file body references a local file; files are never read at import", "Attach the file to the request")
		return impkit.MustJSON(map[string]any{"mode": "file", "file": map[string]string{"src": ""}})
	case text == "" && mt == "":
		return nil
	}
	return impkit.RawBody(text, impkit.LanguageFor(mt))
}

func (b *builder) auth(a map[string]any, ctx *tctx) json.RawMessage {
	if len(a) == 0 {
		return nil
	}
	typ := strings.ToLower(str(a, "type"))
	if typ == "" || typ == "none" {
		return nil
	}
	if boolean(a, "disabled") {
		b.add(impkit.Degraded, ctx.path, ctx.uid, "auth:disabled", "disabled "+typ+" auth was not imported", "")
		return nil
	}
	cred := func(what, v string) {
		if v != "" && !strings.Contains(v, "{{") {
			impkit.CountCredential(b.rep(), ctx.path, ctx.uid, "auth "+typ+"."+what)
		}
	}
	switch typ {
	case "basic", "digest", "ntlm":
		u, p := ctx.conv(str(a, "username")), ctx.conv(str(a, "password"))
		cred("password", p)
		if typ != "basic" {
			b.add(impkit.PreservedInert, ctx.path, ctx.uid, "auth:"+typ, "auth type "+typ+" is kept but not applied when sending", "Add the header manually")
		}
		return impkit.AuthObj(typ, [][2]string{{"username", u}, {"password", p}})
	case "bearer":
		t := ctx.conv(str(a, "token"))
		cred("token", t)
		if pf := str(a, "prefix"); pf != "" && !strings.EqualFold(pf, "Bearer") {
			b.add(impkit.Degraded, ctx.path, ctx.uid, "auth:bearer-prefix", "custom bearer prefix "+pf+" is not applied; Bearer is used", "Set the Authorization header explicitly")
		}
		return impkit.AuthObj("bearer", [][2]string{{"token", t}})
	case "apikey":
		in := "header"
		if strings.EqualFold(str(a, "addTo"), "queryParams") {
			in = "query"
		}
		v := ctx.conv(str(a, "value"))
		cred("value", v)
		return impkit.AuthObj("apikey", [][2]string{{"key", ctx.conv(str(a, "key"))}, {"value", v}, {"in", in}})
	case "oauth2", "oauth1", "hawk", "iam", "aws-iam", "asap", "netrc":
		kv := [][2]string{}
		for _, k := range sortedKeys(a) {
			if k == "type" {
				continue
			}
			if s, ok := a[k].(string); ok {
				if impkit.IsSecretKey(k) {
					cred(k, s)
				}
				kv = append(kv, [2]string{k, s})
			}
		}
		b.add(impkit.PreservedInert, ctx.path, ctx.uid, "auth:"+typ, "auth type "+typ+" is kept but not applied when sending (token fetch flows are not run)", "Obtain a token manually and use bearer auth")
		return impkit.AuthObj(typ, kv)
	}
	b.add(impkit.Unsupported, ctx.path, ctx.uid, "auth:"+typ, "unknown auth type "+typ+" was not imported", "")
	return nil
}

func (b *builder) settings(s map[string]any, path, uid string) json.RawMessage {
	if len(s) == 0 {
		return nil
	}
	out := postman.NewOMap()
	for _, k := range sortedKeys(s) {
		v := fmt.Sprint(s[k])
		switch k {
		case "settingFollowRedirects":
			switch v {
			case "on", "true":
				out.SetValue("followRedirects", true)
			case "off", "false":
				out.SetValue("followRedirects", false)
			}
		case "followRedirects":
			if bv, ok := s[k].(bool); ok {
				out.SetValue("followRedirects", bv)
			}
		default:
			out.SetValue("insomnia."+k, s[k])
		}
	}
	if len(out.Keys()) == 0 {
		return nil
	}
	return impkit.MustJSON(out)
}

func (b *builder) scripts(n *node, it *store.Item, path string) {
	var evs []json.RawMessage
	add := func(listen, src string) {
		if strings.TrimSpace(src) == "" {
			return
		}
		evs = append(evs, impkit.ScriptEvent(listen, "insomnia", src))
		impkit.RecordScript(b.rep(), path, it.UID, listen, "insomnia", src)
	}
	add("prerequest", n.preScript)
	add("test", n.postScript)
	it.Events = impkit.Events(evs)
}

// ---- template conversion ----

var (
	exprRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	tagRe  = regexp.MustCompile(`\{%\s*(\w+)\s*(.*?)\s*%\}`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
)

// tctx collects per-item template findings.
type tctx struct {
	b     *builder
	path  string
	uid   string
	chain []map[string]any
	seen  map[string]bool
}

func (c *tctx) once(feature string) bool {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[feature] {
		return false
	}
	c.seen[feature] = true
	return true
}

// convPlain converts a template outside any item (environment values).
func (b *builder) convPlain(s string) string {
	return (&tctx{b: b, path: "environment"}).conv(s)
}

// conv rewrites Insomnia template syntax to Interseptor variables. Tags that
// cannot be represented are kept literally and reported.
func (c *tctx) conv(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	s = tagRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := tagRe.FindStringSubmatch(m)
		return c.tag(sub[1], sub[2], m)
	})
	return exprRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.TrimSpace(exprRe.FindStringSubmatch(m)[1])
		if strings.HasPrefix(inner, "_.") && nameRe.MatchString(inner[2:]) {
			return "{{" + inner[2:] + "}}"
		}
		if strings.HasPrefix(inner, "$") || (nameRe.MatchString(inner) && !strings.ContainsAny(inner, " |")) {
			return "{{" + inner + "}}"
		}
		if c.once("expr:" + inner) {
			c.b.add(impkit.NeedsReview, c.path, c.uid, "template-expression", "template expression {{ "+impkit.Clip(inner, 80)+" }} has no equivalent and was kept as literal text", "Replace it with a variable")
		}
		return m
	})
}

func (c *tctx) tag(name, args, whole string) string {
	switch name {
	case "uuid":
		return "{{$guid}}"
	case "timestamp":
		return "{{$timestamp}}"
	case "now":
		if strings.Contains(args, "iso") {
			return "{{$isoTimestamp}}"
		}
		if strings.Contains(args, "unix") || strings.Contains(args, "millis") {
			return "{{$timestamp}}"
		}
	case "response":
		return c.response(args, whole)
	}
	if c.once("tag:" + name) {
		c.b.add(impkit.Degraded, c.path, c.uid, "template-tag:"+name, "template tag {% "+name+" %} has no equivalent and was kept as literal text", "Replace it with a variable or a pre-request script")
	}
	return whole
}

// response converts {% response 'body', 'req_id', 'b64::<jsonpath>::46b', ... %}
// into a variable reference plus an extractor descriptor in the item's vars.
func (c *tctx) response(args, whole string) string {
	parts := splitArgs(args)
	if len(parts) < 2 {
		return whole
	}
	c.b.chains++
	name := fmt.Sprintf("chain_%d", c.b.chains)
	d := map[string]any{"name": name, "source": "response", "field": parts[0], "origin": whole}
	if uid, ok := c.b.ids[parts[1]]; ok {
		d["request"] = uid
	} else {
		d["request"] = ""
		d["requestId"] = parts[1]
	}
	if len(parts) > 2 {
		f := parts[2]
		if strings.HasPrefix(f, "b64::") {
			if i := strings.Index(f[5:], "::"); i >= 0 {
				if dec, err := base64.StdEncoding.DecodeString(f[5 : 5+i]); err == nil {
					f = string(dec)
				}
			}
		}
		d["path"] = f
	}
	if len(parts) > 3 {
		d["trigger"] = parts[3]
	}
	c.chain = append(c.chain, d)
	return "{{" + name + "}}"
}

func (c *tctx) finish() {
	for _, d := range c.chain {
		c.b.add(impkit.Degraded, c.path, c.uid, "response-chaining", fmt.Sprintf("response chaining tag became variable {{%s}}; the extractor is stored but not applied until a request sets it", d["name"]), "Add a script or extractor that sets "+fmt.Sprint(d["name"]))
	}
}

// splitArgs splits a tag argument list on commas, honouring single quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		out = append(out, strings.Trim(strings.TrimSpace(cur.String()), `'"`))
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
			cur.WriteByte(ch)
		case ch == '\'' || ch == '"':
			quote = ch
			cur.WriteByte(ch)
		case ch == ',':
			flush()
		default:
			cur.WriteByte(ch)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		flush()
	}
	return out
}

// environments converts the base environment to collection variables and the
// sub-environments to environments.
func (b *builder) environments(p *parsed) {
	if p.base != nil {
		for _, k := range sortedKeys(p.base.data) {
			b.variable(store.VarOwnerCollection, b.res.Collection.UID, k, p.base.data[k], "base environment")
		}
		subs := p.base.subs
		sort.SliceStable(subs, func(i, j int) bool { return subs[i].sortKey < subs[j].sortKey })
		for _, e := range subs {
			b.env(e)
		}
	}
	for _, e := range p.envs {
		b.env(e)
	}
}

func (b *builder) env(e *env) {
	name := e.name
	if name == "" {
		name = "Environment"
	}
	im := postman.EnvImport{Environment: store.Environment{UID: b.opt.NewID(), Name: name, Kind: "env", CollectionUID: b.res.Collection.UID}}
	before := len(b.res.Variables)
	for _, k := range sortedKeys(e.data) {
		b.variable(store.VarOwnerEnvironment, im.Environment.UID, k, e.data[k], "environment "+name)
	}
	im.Variables = append(im.Variables, b.res.Variables[before:]...)
	b.res.Variables = b.res.Variables[:before]
	b.res.Report.Stats.EnvironmentVariables += len(im.Variables)
	if e.private {
		b.add(impkit.NeedsReview, "environment "+name, "", "private-environment", "environment was marked private in Insomnia; it is imported as a normal environment", "Review before sharing the project")
	}
	b.res.Environments = append(b.res.Environments, im)
}
