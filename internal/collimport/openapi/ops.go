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
	enc        bool // key and value are already percent-encoded
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
		raw, has := b.paramRaw(p)
		off := !asBool(p.m.get("required"))
		if p.m.has("content") {
			// A parameter described by "content" is serialized by its media
			// type (usually JSON), not by a style: keep the value as written.
			raw = scalarString(raw)
		}
		switch p.in {
		case "path":
			b.pathVar[p.name] = pathParam(p, raw, has)
		case "query":
			for _, row := range b.queryParam(p, raw) {
				row.disabled = off
				b.query = append(b.query, row)
			}
		case "header":
			switch strings.ToLower(p.name) {
			case "accept", "content-type", "authorization":
				continue // governed by body/security
			}
			b.headers = append(b.headers, kv{key: p.name, value: joinStyle(raw, collectionSep(p), explodes(p, false)), disabled: off})
		case "cookie":
			cookieRows = append(cookieRows, kv{key: p.name, value: scalarString(raw), disabled: off})
		}
	}
	if len(cookieRows) > 0 {
		var parts []string
		for _, c := range cookieRows {
			parts = append(parts, c.key+"="+c.value)
		}
		b.headers = append(b.headers, kv{"Cookie", strings.Join(parts, "; "), true, false})
	}
}

// paramValue is the authored value as one string (form fields).
func (b *reqBuilder) paramValue(p param) string {
	v, _ := b.paramRaw(p)
	return scalarString(v)
}

// paramRaw picks an authored example/default/enum, never a made-up sample. An
// array whose items carry an authored value counts as authored with that one
// element.
func (b *reqBuilder) paramRaw(p param) (any, bool) {
	m := p.m
	if m.has("example") {
		return m.get("example"), true
	}
	if ex := asMap(m.get("examples")); ex != nil && len(ex.keys) > 0 {
		if e, _, err := deref(b.im.root, ex.m[ex.keys[0]]); err == nil {
			if v := asMap(e); v != nil && v.has("value") {
				return v.get("value"), true
			}
		}
	}
	if m.has("x-example") {
		return m.get("x-example"), true
	}
	src := m
	if s, _, err := deref(b.im.root, m.get("schema")); err == nil && asMap(s) != nil {
		src = asMap(s)
	}
	if v, ok := declared(src); ok {
		return v, true
	}
	if schemaType(src) == "array" {
		if it, _, err := deref(b.im.root, src.get("items")); err == nil && asMap(it) != nil {
			if v, ok := declared(asMap(it)); ok {
				return []any{v}, true
			}
		}
	}
	return nil, false
}

// paramStyle is the OpenAPI 3 style of a parameter (Parameter Object, "Style
// Values": query and cookie default to form, path and header to simple).
// Swagger 2 collectionFormat maps onto it: csv is the comma join, ssv and
// pipes the delimited styles, multi the exploded form; tsv has no v3 name.
func paramStyle(p param) string {
	if s := asString(p.m.get("style")); s != "" {
		return s
	}
	switch asString(p.m.get("collectionFormat")) {
	case "ssv":
		return "spaceDelimited"
	case "pipes":
		return "pipeDelimited"
	case "tsv":
		return "tsv"
	}
	if p.in == "query" || p.in == "cookie" {
		return "form"
	}
	return "simple"
}

// collectionSep is the separator of a Swagger 2 header or path array (csv by
// default; multi is not defined outside query and formData).
func collectionSep(p param) string {
	switch asString(p.m.get("collectionFormat")) {
	case "ssv":
		return " "
	case "tsv":
		return "\t"
	case "pipes":
		return "|"
	}
	return ","
}

// explodes reports the explode flag; def is the default when it is absent
// (true for the form style of an OpenAPI 3 document, false everywhere else).
func explodes(p param, def bool) bool {
	if p.m.has("explode") {
		return asBool(p.m.get("explode"))
	}
	if p.m.has("collectionFormat") {
		return asString(p.m.get("collectionFormat")) == "multi"
	}
	return def
}

// joinStyle flattens a value the way the non-exploded styles do: array items
// joined by sep, object properties as key,value,key,value. With pairs an
// object becomes key=value joined by sep (the exploded simple, label and
// matrix forms). Scalars are returned as written.
func joinStyle(v any, sep string, pairs bool) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, scalarString(e))
		}
		return strings.Join(parts, sep)
	case *omap:
		parts := make([]string, 0, 2*len(t.keys))
		for _, k := range t.keys {
			if pairs {
				parts = append(parts, k+"="+scalarString(t.m[k]))
			} else {
				parts = append(parts, k, scalarString(t.m[k]))
			}
		}
		return strings.Join(parts, sep)
	}
	return scalarString(v)
}

// pathParam serializes a path parameter (simple, label, matrix). A parameter
// without an authored value keeps a {{name}} placeholder inside the style's
// prefix.
func pathParam(p param, v any, has bool) string {
	style, ex := paramStyle(p), explodes(p, false)
	val := joinStyle(v, collectionSep(p), ex)
	if !has || val == "" {
		val = "{{" + p.name + "}}"
		v = nil
	}
	_, isArr := v.([]any)
	_, isObj := v.(*omap)
	switch style {
	case "label":
		if ex && (isArr || isObj) {
			val = joinStyle(v, ".", true)
		}
		return "." + val
	case "matrix":
		switch {
		case ex && isObj:
			return ";" + joinStyle(v, ";", true)
		case ex && isArr:
			return ";" + p.name + "=" + joinStyle(v, ";"+p.name+"=", false)
		}
		return ";" + p.name + "=" + val
	}
	return val
}

// queryParam serializes a query parameter into one row, or several when the
// style explodes (form with explode, deepObject). Delimiters the style adds
// are written percent-encoded, as in the specification's "Style Examples"
// table; such rows are marked enc so they are not escaped a second time.
func (b *reqBuilder) queryParam(p param, v any) []kv {
	arr, isArr := v.([]any)
	obj, isObj := v.(*omap)
	one := func(val string, enc bool) []kv { return []kv{{key: p.name, value: val, enc: enc}} }
	if !isArr && !isObj {
		return one(scalarString(v), false)
	}
	switch style := paramStyle(p); style {
	case "spaceDelimited":
		return one(joinStyle(v, "%20", false), true)
	case "pipeDelimited":
		return one(joinStyle(v, "%7C", false), true)
	case "tsv":
		return one(joinStyle(v, "%09", false), true)
	case "deepObject":
		if !isObj || len(obj.keys) == 0 {
			break
		}
		rows := make([]kv, 0, len(obj.keys))
		for _, k := range obj.keys {
			rows = append(rows, kv{key: p.name + "%5B" + k + "%5D", value: scalarString(obj.m[k]), enc: true})
		}
		return rows
	case "form":
		if !explodes(p, !b.im.v2) {
			break
		}
		if isArr && len(arr) > 0 {
			rows := make([]kv, 0, len(arr))
			for _, e := range arr {
				rows = append(rows, kv{key: p.name, value: scalarString(e)})
			}
			return rows
		}
		if isObj && len(obj.keys) > 0 {
			rows := make([]kv, 0, len(obj.keys))
			for _, k := range obj.keys {
				rows = append(rows, kv{key: k, value: scalarString(obj.m[k])})
			}
			return rows
		}
	}
	return one(joinStyle(v, ",", false), false)
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
		switch {
		case q.disabled:
		case q.enc:
			enabled = append(enabled, q.key+"="+q.value)
		default:
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
