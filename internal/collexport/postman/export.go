// Package postman exports collections, environments and globals as Postman
// v2.1 JSON. It is the inverse of internal/collimport/postman: sidecars are
// re-emitted, original exec line arrays and key order are kept verbatim, and
// content Postman cannot represent (isp-only scripts and variable pipes) is
// converted to a disabled explanatory event or reported as a warning.
//
// Secrets: the input bundle MUST come from store.ExportCollectionsBundle with
// the default (scrubbed) options for any export that leaves the machine; that
// is the single scrub function. This package additionally blanks secret-type
// variable values unless IncludeSecrets is set.
package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collection"
	pmimport "github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// SchemaV21 is emitted when a collection has no original schema.
const SchemaV21 = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

// Options for an export.
type Options struct {
	// IncludeSecrets keeps secret-type variable values present in the input.
	// The caller must have obtained explicit confirmation.
	IncludeSecrets bool
	// Compact writes minified JSON instead of tab-indented.
	Compact bool
}

// Warning notes content that does not survive in Postman.
type Warning struct {
	Path    string `json:"path,omitempty"`
	Feature string `json:"feature"`
	Message string `json:"message"`
}

// Output is an exported file.
type Output struct {
	Data     []byte    `json:"-"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// ErrNoCollection is returned when the collection uid is not in the bundle.
var ErrNoCollection = errors.New("postman export: collection not found")

type exporter struct {
	b     store.CollectionsBundle
	opt   Options
	warns []Warning
	vars  map[string][]store.Variable // ownerKind+"/"+uid
}

// ExportCollection writes collection uid from the bundle as Postman v2.1.
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
	e := &exporter{b: b, opt: opt, vars: map[string][]store.Variable{}}
	for _, v := range b.Variables {
		k := v.OwnerKind + "/" + v.OwnerUID
		e.vars[k] = append(e.vars[k], v)
	}
	var items []store.Item
	for _, it := range b.Items {
		if it.CollectionUID == uid {
			items = append(items, it)
		}
	}
	root, wrapped, err := e.collection(col, items)
	if err != nil {
		return nil, err
	}
	var top json.Marshaler = root
	if wrapped {
		w := pmimport.NewOMap()
		raw, _ := root.MarshalJSON()
		w.Set("collection", raw)
		top = w
	}
	return e.finish(top)
}

func (e *exporter) finish(m json.Marshaler) (*Output, error) {
	raw, err := m.MarshalJSON()
	if err != nil {
		return nil, err
	}
	e.scanISP(string(raw))
	if !e.opt.Compact {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "\t"); err != nil {
			return nil, err
		}
		raw = buf.Bytes()
	}
	return &Output{Data: raw, Warnings: e.warns}, nil
}

func (e *exporter) collection(c *store.Collection, items []store.Item) (*pmimport.OMap, bool, error) {
	var side pmimport.CollectionSidecar
	_ = json.Unmarshal(c.Sidecar, &side)
	info := pmimport.NewOMap()
	for k, v := range side.Info {
		info.Set(k, v)
	}
	info.SetValue("name", c.Name)
	if d := descField(c.Description, side.DescriptionRaw); d != nil {
		info.Set("description", d)
	}
	if !info.Has("schema") {
		info.SetValue("schema", SchemaV21)
	}
	info.Order(append(side.InfoKeyOrder, "_postman_id", "name", "description", "schema"))
	root := pmimport.NewOMap()
	infoRaw, _ := info.MarshalJSON()
	root.Set("info", infoRaw)
	tree := collection.BuildTree(items)
	itemsRaw, err := e.itemList(tree, c.Name)
	if err != nil {
		return nil, false, err
	}
	root.Set("item", itemsRaw)
	if ev := e.events(c.Events, c.Name); ev != nil {
		root.Set("event", ev)
	}
	if v := e.mergeVars(side.Variable, e.vars[store.VarOwnerCollection+"/"+c.UID], false); v != nil {
		root.Set("variable", v)
	}
	if !isNull(c.Auth) {
		root.Set("auth", c.Auth)
	}
	if !isNull(c.Settings) {
		root.Set("protocolProfileBehavior", c.Settings)
	}
	for k, v := range side.Extra {
		root.Set(k, v)
	}
	root.Order(keyOrder(c.KeyOrder, []string{"info", "item", "event", "variable", "auth", "protocolProfileBehavior"}))
	return root, side.Wrapped, nil
}

func keyOrder(raw json.RawMessage, def []string) []string {
	var ko []string
	if json.Unmarshal(raw, &ko) == nil && len(ko) > 0 {
		return ko
	}
	return def
}

func (e *exporter) itemList(nodes []*collection.Node, path string) (json.RawMessage, error) {
	var out []json.RawMessage
	for _, n := range nodes {
		m, err := e.item(n, path)
		if err != nil {
			return nil, err
		}
		raw, _ := m.MarshalJSON()
		out = append(out, raw)
	}
	if out == nil {
		out = []json.RawMessage{}
	}
	return pmimport.MarshalArray(out), nil
}

func (e *exporter) item(n *collection.Node, parentPath string) (*pmimport.OMap, error) {
	it := n.Item
	path := parentPath + "/" + it.Name
	var side pmimport.ItemSidecar
	_ = json.Unmarshal(it.Sidecar, &side)
	m := pmimport.NewOMap()
	if side.ID != "" {
		m.SetValue("id", side.ID)
	}
	m.SetValue("name", it.Name)
	ko := keyOrder(it.KeyOrder, nil)
	if !side.DescInRequest {
		if d := descField(it.DescriptionMD, side.DescriptionRaw); d != nil {
			m.Set("description", d)
		} else if contains(ko, "description") {
			m.SetValue("description", it.DescriptionMD)
		}
	}
	if ev := e.events(it.Events, path); ev != nil {
		m.Set("event", ev)
	}
	if !isNull(it.Settings) {
		m.Set("protocolProfileBehavior", it.Settings)
	}
	owner := store.VarOwnerRequest
	if it.Kind == "folder" {
		owner = store.VarOwnerFolder
	}
	if v := e.mergeVars(side.Variable, e.vars[owner+"/"+it.UID], false); v != nil {
		m.Set("variable", v)
	}
	if !isNull(it.Examples) {
		m.Set("response", it.Examples)
	}
	for k, v := range side.Extra {
		m.Set(k, v)
	}
	if it.Kind == "folder" {
		if !isNull(it.Auth) {
			m.Set("auth", it.Auth)
		}
		kids, err := e.itemList(n.Children, path)
		if err != nil {
			return nil, err
		}
		m.Set("item", kids)
		m.Order(append(ko, "id", "name", "description", "item", "event", "variable", "auth"))
		return m, nil
	}
	req, err := e.request(&it, &side)
	if err != nil {
		return nil, err
	}
	m.Set("request", req)
	m.Order(append(ko, "id", "name", "description", "event", "request", "response"))
	return m, nil
}

func (e *exporter) request(it *store.Item, side *pmimport.ItemSidecar) (json.RawMessage, error) {
	if side.Shorthand && it.Method == "GET" && isNull(it.Headers) && isNull(it.Body) && isNull(it.Auth) && len(side.RequestExtra) == 0 && !isNull(it.URL) {
		return it.URL, nil
	}
	r := pmimport.NewOMap()
	r.SetValue("method", it.Method)
	if !isNull(it.Headers) {
		r.Set("header", headers(it.Headers, side.HeaderString))
	}
	if !isNull(it.Body) {
		r.Set("body", it.Body)
	}
	if !isNull(it.URL) {
		r.Set("url", it.URL)
	}
	if !isNull(it.Auth) {
		r.Set("auth", it.Auth)
	}
	switch {
	case side.DescInRequest:
		if d := descField(it.DescriptionMD, side.RequestDescription); d != nil {
			r.Set("description", d)
		}
	case !isNull(side.RequestDescription):
		r.Set("description", side.RequestDescription)
	}
	for k, v := range side.RequestExtra {
		r.Set(k, v)
	}
	r.Order(append(side.RequestKeys, "method", "header", "body", "url", "auth", "description"))
	return r.MarshalJSON()
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// descField re-emits a description: the original object when its content is
// unchanged, the object with updated content, or a plain string.
func descField(md string, raw json.RawMessage) json.RawMessage {
	if !isNull(raw) && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		m, err := pmimport.ParseOMap(raw)
		if err == nil {
			if jsonString(m.Get("content")) == md {
				return raw
			}
			m.SetValue("content", md)
			out, _ := m.MarshalJSON()
			return out
		}
	}
	if !isNull(raw) && md == jsonString(raw) {
		return raw
	}
	if md == "" {
		return nil
	}
	b, _ := pmimport.Marshal(md)
	return b
}

// events re-emits the event array verbatim, converting scripts Postman cannot
// run (isp/starlark) into a disabled explanatory JavaScript event.
func (e *exporter) events(raw json.RawMessage, path string) json.RawMessage {
	if isNull(raw) {
		return nil
	}
	var elems []json.RawMessage
	if json.Unmarshal(raw, &elems) != nil {
		return raw
	}
	changed := false
	for i, el := range elems {
		m, err := pmimport.ParseOMap(el)
		if err != nil {
			continue
		}
		sm, err := pmimport.ParseOMap(m.Get("script"))
		if err != nil {
			continue
		}
		t := jsonString(sm.Get("type"))
		if t == "" || t == "text/javascript" {
			continue
		}
		var lines []string
		if json.Unmarshal(sm.Get("exec"), &lines) != nil {
			lines = []string{jsonString(sm.Get("exec"))}
		}
		out := []string{"// interseptor: this " + t + " script cannot run in Postman; original source follows, commented out"}
		for _, l := range lines {
			out = append(out, "// "+l)
		}
		ns := pmimport.NewOMap()
		ns.SetValue("type", "text/javascript")
		ns.SetValue("exec", out)
		m.Set("script", mustRaw(ns))
		m.SetValue("disabled", true)
		elems[i] = mustRaw(m)
		changed = true
		e.warns = append(e.warns, Warning{Path: path, Feature: "script:" + t, Message: t + " script exported as a disabled explanatory event"})
	}
	if !changed {
		return raw
	}
	return pmimport.MarshalArray(elems)
}

func mustRaw(m *pmimport.OMap) json.RawMessage {
	b, _ := m.MarshalJSON()
	return b
}

// mergeVars overlays the variable table on the original variable[] array
// (sidecar), preserving element key order, ids and extras; deleted variables
// drop out and new ones are appended. nil means "emit no variable key".
func (e *exporter) mergeVars(side json.RawMessage, vars []store.Variable, env bool) json.RawMessage {
	table := map[string]store.Variable{}
	var order []string
	for _, v := range vars {
		if _, ok := table[v.Key]; !ok {
			table[v.Key] = v
			order = append(order, v.Key)
		}
	}
	var out []json.RawMessage
	used := map[string]bool{}
	var elems []json.RawMessage
	_ = json.Unmarshal(side, &elems)
	for _, el := range elems {
		m, err := pmimport.ParseOMap(el)
		if err != nil {
			continue
		}
		key := jsonString(m.Get("key"))
		if key == "" {
			key = jsonString(m.Get("id"))
		}
		tv, ok := table[key]
		if key != "" && (!ok || used[key]) {
			continue
		}
		if ok {
			used[key] = true
			e.applyVar(m, tv, env)
		}
		out = append(out, mustRaw(m))
	}
	for _, k := range order {
		if used[k] {
			continue
		}
		m := pmimport.NewOMap()
		m.SetValue("key", k)
		e.applyVar(m, table[k], env)
		out = append(out, mustRaw(m))
	}
	if out == nil {
		if isNull(side) {
			return nil
		}
		out = []json.RawMessage{}
	}
	return pmimport.MarshalArray(out)
}

func (e *exporter) applyVar(m *pmimport.OMap, v store.Variable, env bool) {
	origType := jsonString(m.Get("type"))
	if mapType(origType) != v.Type || !m.Has("type") && (env || v.Type != store.VarTypeDefault) {
		switch {
		case v.Type == store.VarTypeDefault && !env:
			m.SetValue("type", "string")
		default:
			m.SetValue("type", v.Type)
		}
	}
	val := v.InitialValue
	if v.Type == store.VarTypeSecret && !e.opt.IncludeSecrets {
		val = ""
	}
	if !m.Has("value") || valueString(m.Get("value")) != val {
		m.SetValue("value", val)
	}
	if m.Has("enabled") || env {
		if cur, ok := boolOf(m.Get("enabled")); !ok || cur != v.Enabled {
			m.SetValue("enabled", v.Enabled)
		}
	} else if cur, ok := boolOf(m.Get("disabled")); (ok && cur == v.Enabled) || (!ok && !v.Enabled) {
		m.SetValue("disabled", !v.Enabled)
	}
}

func mapType(t string) string {
	switch t {
	case "secret":
		return store.VarTypeSecret
	case "any":
		return store.VarTypeAny
	}
	return store.VarTypeDefault
}

func boolOf(raw json.RawMessage) (bool, bool) {
	var b bool
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

func valueString(raw json.RawMessage) string {
	if isNull(raw) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// ExportEnvironment writes an environment or globals file. sidecar is the
// EnvImport.Sidecar from the importer (nil for natively created environments).
func ExportEnvironment(env store.Environment, vars []store.Variable, sidecar json.RawMessage, opt Options) (*Output, error) {
	e := &exporter{opt: opt}
	var side pmimport.EnvSidecar
	_ = json.Unmarshal(sidecar, &side)
	m := pmimport.NewOMap()
	for k, v := range side.Extra {
		m.Set(k, v)
	}
	if !m.Has("id") {
		m.SetValue("id", env.UID)
	}
	m.SetValue("name", env.Name)
	scope := "environment"
	if env.Kind == "globals" {
		scope = "globals"
	}
	m.SetValue("_postman_variable_scope", scope)
	vals := e.mergeVars(side.Values, vars, true)
	if vals == nil {
		vals = json.RawMessage("[]")
	}
	m.Set("values", vals)
	m.Order(append(side.Keys, "id", "name", "values", "_postman_variable_scope"))
	return e.finish(m)
}

var (
	pipeRe   = regexp.MustCompile(`\{\{[^{}]*\|[^{}]*\}\}`)
	ispVarRe = regexp.MustCompile(`\{\{\s*\$(oob|payload)[^{}]*\}\}`)
)

// scanISP warns about Interseptor-only syntax Postman will not understand.
func (e *exporter) scanISP(s string) {
	seen := map[string]bool{}
	var found []string
	for _, re := range []*regexp.Regexp{pipeRe, ispVarRe} {
		for _, m := range re.FindAllString(s, -1) {
			if !seen[m] {
				seen[m] = true
				found = append(found, m)
			}
		}
	}
	sort.Strings(found)
	for _, m := range found {
		e.warns = append(e.warns, Warning{Feature: "isp-only-syntax", Message: fmt.Sprintf("%s is Interseptor-only and will stay unresolved in Postman", m)})
	}
	if strings.Contains(s, "isp.") && strings.Contains(s, `"exec"`) {
		e.warns = append(e.warns, Warning{Feature: "isp-api", Message: "scripts reference isp.* APIs that do not exist in Postman"})
	}
}

// headers re-emits the v2.0 string form when the source used it and every row
// can be written as a "Key: Value" line; otherwise the list form.
func headers(h json.RawMessage, asString bool) json.RawMessage {
	if !asString {
		return h
	}
	var rows []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled"`
	}
	if json.Unmarshal(h, &rows) != nil {
		return h
	}
	var lines []string
	for _, r := range rows {
		if r.Disabled {
			return h
		}
		lines = append(lines, r.Key+": "+r.Value)
	}
	b, _ := pmimport.Marshal(strings.Join(lines, "\n"))
	return b
}
