// Package insomnia imports Insomnia exports (v3/v4 JSON "resources" exports and
// v5 YAML collections) into the collection model. Nothing is executed,
// fetched or read from disk: scripts are recorded and quarantined, template
// tags are converted to variables or reported, and anything without a
// faithful equivalent lands in the honest unsupported report.
package insomnia

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxInputBytes = impkit.MaxInputBytes
	MaxDepth      = impkit.MaxDepth
	MaxItems      = impkit.MaxItems
)

// Errors.
var (
	ErrTooLarge     = errors.New("insomnia: input exceeds 64 MiB")
	ErrNotInsomnia  = errors.New("insomnia: not an Insomnia export")
	ErrNothingFound = errors.New("insomnia: export holds no requests")
)

// Options tune a parse. NewID defaults to store.NewUID.
type Options struct {
	NewID func() string
	Name  string // overrides the collection name
}

// Result is the parsed import, ready to preview and commit.
type Result struct {
	Collection   store.Collection      `json:"collection"`
	Items        []store.Item          `json:"items"`
	Variables    []store.Variable      `json:"variables"`
	Environments []postman.EnvImport   `json:"environments,omitempty"`
	Report       impkit.Report         `json:"report"`
	SecretValues []postman.SecretValue `json:"-"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	b := store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}, Items: append([]store.Item(nil), r.Items...),
		Variables: append([]store.Variable(nil), r.Variables...)}
	for _, e := range r.Environments {
		b.Environments = append(b.Environments, e.Environment)
		b.Variables = append(b.Variables, e.Variables...)
	}
	return b
}

// node is the format-independent tree built from v4 resources or v5 YAML.
type node struct {
	id, name, desc string
	id2            string // assigned uid
	folder         bool
	method, url    string
	params         []impkit.Row
	headers        []impkit.Row
	body           map[string]any
	auth           map[string]any
	env            map[string]any // folder-level variables
	preScript      string
	postScript     string
	settings       map[string]any
	sortKey        float64
	extra          []string // dropped property names
	children       []*node
}

type env struct {
	id, name  string
	data      map[string]any
	private   bool
	sortKey   float64
	subs      []*env
	isDefault bool
}

type parsed struct {
	name    string
	root    []*node
	envs    []*env // base environment first (if any), then others
	base    *env
	skipped map[string]int // unsupported resource type -> count
	notes   []impkit.Entry
}

// Parse reads an Insomnia export.
func Parse(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	root, err := decode(data)
	if err != nil {
		return nil, err
	}
	var p *parsed
	switch {
	case isV5(root):
		p, err = parseV5(root)
	case isV4(root):
		p, err = parseV4(root)
	default:
		return nil, ErrNotInsomnia
	}
	if err != nil {
		return nil, err
	}
	return build(p, opt)
}

func decode(data []byte) (map[string]any, error) {
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 {
		return nil, ErrNotInsomnia
	}
	var root any
	if trim[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			return nil, fmt.Errorf("insomnia: invalid JSON: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(trim, &root); err != nil {
			return nil, fmt.Errorf("insomnia: invalid YAML: %w", err)
		}
		root = normalizeYAML(root, 0)
	}
	m, ok := root.(map[string]any)
	if !ok {
		return nil, ErrNotInsomnia
	}
	return m, nil
}

// normalizeYAML converts map[any]any-style trees into JSON-shaped ones and
// caps depth so a hostile document cannot recurse the walkers.
func normalizeYAML(v any, depth int) any {
	if depth > 256 {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeYAML(x, depth+1)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[fmt.Sprint(k)] = normalizeYAML(x, depth+1)
		}
		return out
	case []any:
		for i, x := range t {
			t[i] = normalizeYAML(x, depth+1)
		}
		return t
	}
	return v
}

func isV4(m map[string]any) bool {
	_, ok := m["resources"].([]any)
	return ok && (str(m, "_type") == "export" || m["__export_format"] != nil)
}

func isV5(m map[string]any) bool {
	return strings.HasPrefix(str(m, "type"), "collection.insomnia.rest/")
}

func build(p *parsed, opt Options) (*Result, error) {
	res := &Result{}
	res.Report.Format = "insomnia"
	name := p.name
	if opt.Name != "" {
		name = opt.Name
	}
	if name == "" {
		name = "Insomnia import"
	}
	res.Collection = store.Collection{UID: opt.NewID(), Name: name, ScopePolicy: store.ScopePolicyBlock,
		Sidecar: impkit.MustJSON(postman.CollectionSidecar{Format: "insomnia"})}
	b := &builder{res: res, opt: opt, ids: map[string]string{}}
	b.assign(p.root, 0)
	prev := ""
	for _, n := range sortNodes(p.root) {
		prev = b.emit(n, "", prev, "", 0, nil)
	}
	b.environments(p)
	for _, e := range p.notes {
		res.Report.Entries = append(res.Report.Entries, e)
	}
	for _, t := range sortedKeys(p.skipped) {
		lvl, msg := skippedLevel(t, p.skipped[t])
		res.Report.Entries = append(res.Report.Entries, impkit.Entry{Level: lvl, Feature: "resource:" + t, Message: msg})
	}
	impkit.Finish(&res.Report, "Insomnia")
	res.Collection.ImportReport = impkit.MustJSON(res.Report)
	if res.Report.Stats.Requests == 0 {
		return res, ErrNothingFound
	}
	return res, nil
}

func skippedLevel(t string, n int) (impkit.Level, string) {
	switch t {
	case "grpc_request", "websocket_request", "websocket_payload", "socketio_request", "mock_server", "mock_route":
		return impkit.Unsupported, fmt.Sprintf("%d %s resource(s) not imported (gRPC, WebSocket and mock servers are out of scope)", n, t)
	case "cookie_jar":
		return impkit.Degraded, "cookie jar not imported (cookies are local session state and never imported)"
	}
	return impkit.Degraded, fmt.Sprintf("%d %s resource(s) ignored", n, t)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortNodes(ns []*node) []*node {
	out := append([]*node(nil), ns...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].sortKey < out[j].sortKey })
	return out
}

// ---- small decoding helpers over generic trees ----

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool, int, int64, float64:
		return fmt.Sprint(v)
	}
	return ""
}

func num(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func boolean(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

func arr(m map[string]any, k string) []any {
	a, _ := m[k].([]any)
	return a
}

func rows(v []any) []impkit.Row {
	var out []impkit.Row
	for _, e := range v {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		r := impkit.Row{Key: str(m, "name"), Value: str(m, "value"), Disabled: boolean(m, "disabled")}
		if r.Key == "" {
			r.Key = str(m, "key")
		}
		switch str(m, "type") {
		case "file":
			r.Type, r.Src = "file", ""
			if fn := str(m, "fileName"); fn != "" {
				r.Value = fn
			}
		}
		if ct := str(m, "multiline"); ct != "" && ct != "false" && r.Type == "" {
			r.Type = "text"
		}
		out = append(out, r)
	}
	return out
}
