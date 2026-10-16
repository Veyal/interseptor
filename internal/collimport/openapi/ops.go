package openapi

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

var pathParamRe = regexp.MustCompile(`\{([^}/]+)\}`)

type opInfo struct {
	path, method string
	op, item     *omap
}

// operations walks paths, builds tag folders and request items.
func (im *importer) operations() error {
	paths := asMap(im.root.get("paths"))
	var ops []opInfo
	for _, p := range paths.keysOrNil() {
		item, _, err := deref(im.root, paths.m[p])
		im2 := asMap(item)
		if err != nil || im2 == nil {
			im.reportOnce(p, Blocked, p, "path-ref", "path item "+p+" could not be resolved ("+errString(err)+")", "")
			continue
		}
		for _, m := range httpMethods {
			if op := asMap(im2.get(m)); op != nil {
				if len(ops) >= MaxOperations {
					im.report(Blocked, "paths", "too-many-operations", "only the first "+itoa(MaxOperations)+" operations were imported", "")
					goto built
				}
				ops = append(ops, opInfo{p, m, op, im2})
			}
		}
	}
built:
	folders := im.makeFolders(ops)
	rootPrev := ""
	for _, f := range folders.order {
		rootPrev = folders.byRank[f]
	}
	prev := map[string]string{"": rootPrev}
	for _, o := range ops {
		parent := ""
		if t := firstTag(o.op); t != "" {
			parent = folders.uid[t]
		}
		rank := store.RankBetween(prev[parent], "")
		prev[parent] = rank
		it := im.buildRequest(o, parent, rank)
		im.res.Items = append(im.res.Items, it)
		im.res.Report.Stats.Requests++
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return "not an object"
	}
	return err.Error()
}

func firstTag(op *omap) string {
	for _, t := range asSlice(op.get("tags")) {
		if s := asString(t); s != "" {
			return s
		}
	}
	return ""
}

type folderSet struct {
	order  []string
	uid    map[string]string
	byRank map[string]string
}

// makeFolders creates one folder per first-tag in the order of the top-level
// tags list, then in encounter order.
func (im *importer) makeFolders(ops []opInfo) folderSet {
	fs := folderSet{uid: map[string]string{}, byRank: map[string]string{}}
	used := map[string]bool{}
	var encounter []string
	for _, o := range ops {
		if t := firstTag(o.op); t != "" && !used[t] {
			used[t] = true
			encounter = append(encounter, t)
		}
	}
	desc := map[string]string{}
	var ordered []string
	seen := map[string]bool{}
	for _, t := range asSlice(im.root.get("tags")) {
		tm := asMap(t)
		n := asString(tm.get("name"))
		if n == "" {
			continue
		}
		desc[n] = asString(tm.get("description"))
		if used[n] && !seen[n] {
			seen[n] = true
			ordered = append(ordered, n)
		}
	}
	for _, t := range encounter {
		if !seen[t] {
			seen[t] = true
			ordered = append(ordered, t)
		}
	}
	prev := ""
	for _, name := range ordered {
		rank := store.RankBetween(prev, "")
		prev = rank
		f := store.Item{UID: im.opt.NewID(), CollectionUID: im.res.Collection.UID, Kind: "folder", Rank: rank, Name: name, DescriptionMD: desc[name]}
		im.res.Items = append(im.res.Items, f)
		im.res.Report.Stats.Folders++
		fs.order = append(fs.order, name)
		fs.uid[name] = f.UID
		fs.byRank[name] = rank
	}
	return fs
}

type param struct {
	name, in string
	m        *omap
}

func (im *importer) buildRequest(o opInfo, parent, rank string) store.Item {
	label := strings.ToUpper(o.method) + " " + o.path
	it := store.Item{UID: im.opt.NewID(), CollectionUID: im.res.Collection.UID, ParentUID: parent, Kind: "request",
		Rank: rank, Method: strings.ToUpper(o.method)}
	it.Name = asString(o.op.get("summary"))
	if it.Name == "" {
		it.Name = asString(o.op.get("operationId"))
	}
	if it.Name == "" {
		it.Name = label
	}
	it.DescriptionMD = asString(o.op.get("description"))
	issue := func(f, m string) { im.reportOnce(label+"|"+f, Degraded, label, f, m, "") }
	g := newGen(im.root, true, issue)

	params := im.params(o, label)
	b := &reqBuilder{im: im, it: &it, o: o, label: label, gen: g}
	b.parameters(params)
	b.url()
	b.body(params)
	it.Headers = b.headersJSON()
	it.Examples = b.examples()
	if o.op.has("security") {
		it.Auth = im.authFor(asSlice(o.op.get("security")), label)
	}
	tags := []string{}
	for _, t := range asSlice(o.op.get("tags")) {
		if s := asString(t); s != "" {
			tags = append(tags, s)
		}
	}
	if asBool(o.op.get("deprecated")) {
		tags = append(tags, "deprecated")
		im.reportOnce(label, Degraded, label, "deprecated", "operation is marked deprecated (tagged \"deprecated\")", "")
	}
	if len(tags) > 0 {
		it.Tags = mustJSON(tags)
	}
	side := newOmap()
	spec := newOmap()
	spec.set("path", o.path)
	spec.set("method", o.method)
	if id := asString(o.op.get("operationId")); id != "" {
		spec.set("operationId", id)
	}
	side.set("openapi", spec)
	it.Sidecar = mustJSON(side)
	return it
}

// params merges path-level and operation-level parameters (operation wins).
func (im *importer) params(o opInfo, label string) []param {
	var out []param
	idx := map[string]int{}
	add := func(list []any) {
		for _, raw := range list {
			if len(out) >= MaxParamsPerOp {
				return
			}
			v, _, err := deref(im.root, raw)
			m := asMap(v)
			if err != nil || m == nil {
				im.reportOnce(label+"|param", NeedsReview, label, "param-ref", "a parameter reference could not be resolved ("+errString(err)+") and was skipped", "")
				continue
			}
			p := param{name: asString(m.get("name")), in: asString(m.get("in")), m: m}
			k := p.in + "|" + p.name
			if i, ok := idx[k]; ok {
				out[i] = p
				continue
			}
			idx[k] = len(out)
			out = append(out, p)
		}
	}
	add(asSlice(o.item.get("parameters")))
	add(asSlice(o.op.get("parameters")))
	return out
}

type kv struct {
	key, value string
	disabled   bool
}

type reqBuilder struct {
	im      *importer
	it      *store.Item
	o       opInfo
	label   string
	gen     *gen
	headers []kv
	query   []kv
	pathVar map[string]string
	cookies []string
	prefix  string
}

func (b *reqBuilder) parameters(ps []param) {
	b.pathVar = map[string]string{}
	var cookieRows []kv
	for _, p := range ps {
		val := b.paramValue(p)
		switch p.in {
		case "path":
			if val == "" {
				val = "{{" + p.name + "}}"
			}
			b.pathVar[p.name] = val
		case "query":
			b.query = append(b.query, kv{p.name, val, !asBool(p.m.get("required"))})
		case "header":
			switch strings.ToLower(p.name) {
			case "accept", "content-type", "authorization":
				continue // governed by body/security
			}
			b.headers = append(b.headers, kv{p.name, val, !asBool(p.m.get("required"))})
		case "cookie":
			cookieRows = append(cookieRows, kv{p.name, val, !asBool(p.m.get("required"))})
		}
	}
	if len(cookieRows) > 0 {
		var parts []string
		for _, c := range cookieRows {
			parts = append(parts, c.key+"="+c.value)
		}
		b.headers = append(b.headers, kv{"Cookie", strings.Join(parts, "; "), true})
	}
}

// paramValue picks an authored example/default/enum, never a made-up sample.
func (b *reqBuilder) paramValue(p param) string {
	m := p.m
	if m.has("example") {
		return scalarString(m.get("example"))
	}
	if ex := asMap(m.get("examples")); ex != nil && len(ex.keys) > 0 {
		if e, _, err := deref(b.im.root, ex.m[ex.keys[0]]); err == nil {
			if v := asMap(e); v != nil && v.has("value") {
				return scalarString(v.get("value"))
			}
		}
	}
	if m.has("x-example") {
		return scalarString(m.get("x-example"))
	}
	src := m
	if s, _, err := deref(b.im.root, m.get("schema")); err == nil && asMap(s) != nil {
		src = asMap(s)
	}
	if v, ok := declared(src); ok {
		return scalarString(v)
	}
	return ""
}

func (b *reqBuilder) serverPrefix() string {
	for _, holder := range []*omap{b.o.op, b.o.item} {
		if srv := asSlice(holder.get("servers")); len(srv) > 0 {
			m := asMap(srv[0])
			u := expandServerVars(asString(m.get("url")), asMap(m.get("variables")))
			if u != "" {
				b.im.reportOnce(b.label, Degraded, b.label, "operation-servers", "operation overrides the server; its first URL was used literally", "")
				return strings.TrimRight(u, "/")
			}
		}
	}
	return "{{baseUrl}}"
}

func (b *reqBuilder) url() {
	b.prefix = b.serverPrefix()
	conv := pathParamRe.ReplaceAllString(b.o.path, ":$1")
	raw := b.prefix + conv
	var enabled []string
	for _, q := range b.query {
		if !q.disabled {
			enabled = append(enabled, url.QueryEscape(q.key)+"="+url.QueryEscape(q.value))
		}
	}
	if len(enabled) > 0 {
		raw += "?" + strings.Join(enabled, "&")
	}
	m := postman.NewOMap()
	m.SetValue("raw", raw)
	m.SetValue("host", []string{b.prefix})
	var segs []string
	for _, s := range strings.Split(conv, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	m.SetValue("path", segs)
	if len(b.query) > 0 {
		rows := make([]map[string]any, 0, len(b.query))
		for _, q := range b.query {
			row := map[string]any{"key": q.key, "value": q.value}
			if q.disabled {
				row["disabled"] = true
			}
			rows = append(rows, row)
		}
		m.SetValue("query", rows)
	}
	var vars []map[string]string
	for _, name := range pathNames(b.o.path) {
		v, ok := b.pathVar[name]
		if !ok {
			v = "{{" + name + "}}"
		}
		vars = append(vars, map[string]string{"key": name, "value": v})
	}
	if len(vars) > 0 {
		m.SetValue("variable", vars)
	}
	b.it.URL = mustJSON(m)
}

func pathNames(p string) []string {
	var out []string
	for _, m := range pathParamRe.FindAllStringSubmatch(p, -1) {
		out = append(out, m[1])
	}
	return out
}

func (b *reqBuilder) headersJSON() json.RawMessage {
	rows := make([]map[string]any, 0, len(b.headers))
	for _, h := range b.headers {
		row := map[string]any{"key": h.key, "value": h.value}
		if h.disabled {
			row["disabled"] = true
		}
		rows = append(rows, row)
	}
	return mustJSON(rows)
}

func (b *reqBuilder) addHeader(name, value string) {
	for _, h := range b.headers {
		if strings.EqualFold(h.key, name) {
			return
		}
	}
	b.headers = append(b.headers, kv{key: name, value: value})
}

func prettyJSON(v any) string {
	raw, err := marshalNoEscape(v)
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

func statusCode(code string) int {
	n, err := strconv.Atoi(code)
	if err != nil || n < 100 || n > 599 {
		return 0
	}
	return n
}
