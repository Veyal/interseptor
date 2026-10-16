// Package graphql imports a GraphQL schema as a collection of ready-to-send
// requests. Two inputs are accepted and produce the same output for the same
// schema: an introspection result (the JSON response to the standard
// introspection query, either {"data":{"__schema":...}} or a bare
// {"__schema":...}) and SDL (.graphql / .gql / .graphqls type-system text,
// including "schema {}" and "extend type").
//
// Every root field of Query, Mutation and Subscription becomes one
// "POST {{baseUrl}}/graphql" request with a graphql body: an operation whose
// variables mirror the field arguments (with correct type signatures) and a
// valid, bounded selection set; the requests are grouped into Queries,
// Mutations and Subscriptions folders. The introspection query itself is
// emitted as the first, top-level request. Deprecated fields are kept and
// flagged in the request description. Subscriptions are emitted but reported
// as degraded (they normally run over WebSocket or SSE, not a plain POST).
//
// Limits (all enforced, each with a test): input 32 MiB; 5000 generated
// requests (the rest are dropped and reported "blocked"); 50000 types and
// 500000 fields; type references nested at most 16 deep; selection depth 3 by
// default and never more than 5; at most 400 selected fields per request and
// 25 inline fragments per interface or union.
//
// Parsing is data-only: nothing is executed, fetched or read from disk.
package graphql

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Bounds.
const (
	MaxInputBytes = 32 << 20
	MaxRequests   = 5000
)

// Errors. Suggested dispatcher mapping: ErrTooLarge -> 413, ErrNotGraphQL ->
// 415, ErrSyntax and ErrTooComplex -> 400 / 422.
var (
	ErrTooLarge   = errors.New("graphql: input exceeds 32 MiB")
	ErrNotGraphQL = errors.New("graphql: not an introspection result or GraphQL SDL")
	ErrSyntax     = errors.New("graphql: malformed schema")
	ErrTooComplex = errors.New("graphql: schema exceeds the import limits")
)

// Options tune a parse. NewID defaults to store.NewUID.
type Options struct {
	NewID func() string
	Name  string // collection name (default "GraphQL import")
	// Endpoint is the URL every request posts to (default "{{baseUrl}}/graphql").
	Endpoint string
	// BaseURL is the initial value of the baseUrl collection variable, created
	// only when Endpoint references {{baseUrl}} (default "https://example.com").
	BaseURL string
	// MaxDepth bounds selection-set nesting (default 3, hard cap 5).
	MaxDepth int
}

// Result is the shared importer result type.
type Result = postman.Result

var (
	sdlRoot = regexp.MustCompile(`(?m)^[ \t]*(?:extend[ \t]+)?type[ \t]+(?:Query|Mutation|Subscription)\b|^[ \t]*(?:extend[ \t]+)?schema[ \t]*(?:@|\{)`)
	sdlType = regexp.MustCompile(`(?m)^[ \t]*(?:extend[ \t]+)?(?:type|interface|input)[ \t]+[_A-Za-z]\w*[^\n]*\{`)
)

// Sniff reports whether data looks like a GraphQL introspection result or SDL.
// It is cheap (looks only at the first 64 KiB for SDL) and never errors.
func Sniff(data []byte) bool {
	d := bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n")
	if len(d) == 0 {
		return false
	}
	if d[0] == '{' {
		return bytes.Contains(d, []byte(`"__schema"`)) && bytes.Contains(d, []byte(`"queryType"`))
	}
	if len(d) > 64<<10 {
		d = d[:64<<10]
	}
	return sdlRoot.Match(d) || sdlType.Match(d)
}

// Parse imports an introspection result or SDL document.
func Parse(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	data = bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n")
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrNotGraphQL)
	}
	var (
		s   *schemaDef
		err error
	)
	if data[0] == '{' {
		s, err = decodeIntrospection(data)
	} else {
		s, err = parseSDL(string(data))
	}
	if err != nil {
		return nil, err
	}
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	return build(s, opt), nil
}

type builder struct {
	opt  Options
	res  *Result
	g    *gen
	cuid string
	prev map[string]string // parent uid -> last rank
	reqs int
}

func build(s *schemaDef, opt Options) *Result {
	b := &builder{opt: opt, res: &Result{Kind: postman.KindCollection}, prev: map[string]string{}}
	depth := opt.MaxDepth
	if depth <= 0 {
		depth = DefaultDepth
	}
	clamped := depth > MaxDepthCap
	if clamped {
		depth = MaxDepthCap
	}
	b.g = &gen{s: s, maxDepth: depth}
	name := opt.Name
	if name == "" {
		name = "GraphQL import"
	}
	endpoint := opt.Endpoint
	if endpoint == "" {
		endpoint = "{{baseUrl}}/graphql"
	}
	b.cuid = opt.NewID()
	rep := &b.res.Report
	rep.Format = "graphql"
	b.res.Collection = store.Collection{UID: b.cuid, Name: name, ScopePolicy: store.ScopePolicyBlock,
		Sidecar: impkit.MustJSON(postman.CollectionSidecar{Format: "graphql"})}
	if strings.Contains(endpoint, "{{baseUrl}}") {
		base := opt.BaseURL
		if base == "" {
			base = "https://example.com"
		}
		b.res.Variables = append(b.res.Variables, store.Variable{OwnerKind: store.VarOwnerCollection, OwnerUID: b.cuid,
			Key: "baseUrl", Type: store.VarTypeDefault, InitialValue: base, Enabled: true})
		rep.Stats.Variables++
	}
	if clamped {
		b.add(impkit.Degraded, "", "", "max-depth", fmt.Sprintf("requested selection depth %d exceeds the cap; %d was used", opt.MaxDepth, MaxDepthCap), "")
	}

	b.request("", "Introspection query", "The standard introspection query. Send it first to learn the schema; if the server disables introspection, try field suggestions (\"Did you mean ...\") or the aliases and batching tests.",
		endpoint, introspectionQuery, "{}")

	roots := []struct{ folder, typeName, op string }{
		{"Queries", s.query, "query"},
		{"Mutations", s.mutation, "mutation"},
		{"Subscriptions", s.subscription, "subscription"},
	}
	total := 0
	for _, r := range roots {
		if r.typeName == "" {
			continue
		}
		td := s.types[r.typeName]
		if td == nil || (td.kind != kObject) {
			b.add(impkit.NeedsReview, r.folder, "", "root-type", fmt.Sprintf("root type %q is not defined as an object type in the schema; no %s were generated", r.typeName, strings.ToLower(r.folder)), "")
			continue
		}
		var folder string
		for _, f := range td.fields {
			if strings.HasPrefix(f.name, "__") {
				continue
			}
			if b.reqs >= MaxRequests {
				total++
				continue
			}
			if folder == "" {
				folder = b.folder(r.folder, "Root type "+td.name)
			}
			b.rootField(folder, r.folder, r.op, td, f, endpoint)
		}
	}
	if total > 0 {
		b.add(impkit.Blocked, "", "", "too-many-requests", fmt.Sprintf("only the first %d root fields were imported; %d more were dropped", MaxRequests, total), "Import a trimmed schema, or split it by root type")
	}
	if b.reqs == 0 {
		b.add(impkit.NeedsReview, "", "", "no-root-fields", "the schema defines no Query, Mutation or Subscription fields; only the introspection request was generated", "Check the root type names, or import the full introspection result")
	} else {
		b.add(impkit.Converted, "", "", "root-fields", fmt.Sprintf("%d root field(s) became requests", b.reqs), "")
	}
	b.summarise()
	impkit.Finish(rep, "GraphQL")
	b.res.Collection.ImportReport = impkit.MustJSON(*rep)
	return b.res
}

func (b *builder) add(l impkit.Level, path, uid, feature, msg, sugg string) {
	b.res.Report.Entries = append(b.res.Report.Entries, impkit.Entry{Level: l, Path: path, Item: uid, Feature: feature, Message: msg, Suggestion: sugg})
}

func (b *builder) summarise() {
	g := b.g
	if g.depthCut > 0 {
		b.add(impkit.Degraded, "", "", "selection-depth", fmt.Sprintf("%d object field(s) were not expanded because selection depth is limited to %d", g.depthCut, g.maxDepth), "Raise the depth (maximum 5) or extend the selection by hand")
	}
	if g.recCut > 0 {
		b.add(impkit.Degraded, "", "", "selection-recursion", fmt.Sprintf("%d field(s) were not expanded because their type is already being expanded on the same path (recursive types)", g.recCut), "")
	}
	if g.argCut > 0 {
		b.add(impkit.Degraded, "", "", "selection-required-args", fmt.Sprintf("%d nested field(s) were omitted from selections because they require arguments", g.argCut), "Add them by hand with arguments")
	}
	if g.budgetCut > 0 {
		b.add(impkit.Degraded, "", "", "selection-size", fmt.Sprintf("%d field(s) were omitted because a selection reached %d fields", g.budgetCut, maxSelectionNodes), "")
	}
	if g.fragCut > 0 {
		b.add(impkit.Degraded, "", "", "selection-types", fmt.Sprintf("%d implementing type(s) were left out of interface/union selections (limit %d per type)", g.fragCut, maxAbstractTypes), "")
	}
}

func (b *builder) rank(parent string) string {
	r := store.RankBetween(b.prev[parent], "")
	b.prev[parent] = r
	return r
}

func (b *builder) folder(name, desc string) string {
	uid := b.opt.NewID()
	b.res.Items = append(b.res.Items, store.Item{UID: uid, CollectionUID: b.cuid, Kind: "folder", Name: name,
		DescriptionMD: desc, Rank: b.rank(""), Sidecar: impkit.MustJSON(postman.ItemSidecar{})})
	b.res.Report.Stats.Folders++
	return uid
}

// request appends one POST request and returns its uid.
func (b *builder) request(parent, name, desc, endpoint, query, vars string) string {
	uid := b.opt.NewID()
	b.res.Items = append(b.res.Items, store.Item{UID: uid, CollectionUID: b.cuid, ParentUID: parent, Kind: "request",
		Name: name, DescriptionMD: desc, Rank: b.rank(parent), Method: "POST",
		URL:     impkit.URLObject(endpoint, nil),
		Headers: impkit.Rows([]impkit.Row{{Key: "Content-Type", Value: "application/json"}}),
		Body:    impkit.MustJSON(map[string]any{"mode": "graphql", "graphql": map[string]string{"query": query, "variables": vars}}),
		Sidecar: impkit.MustJSON(postman.ItemSidecar{})})
	b.res.Report.Stats.Requests++
	return uid
}

func (b *builder) rootField(folder, folderName, op string, td *typeDef, f *fieldDef, endpoint string) {
	g := b.g
	g.resetBudget()
	var decl, call, sig []string
	var vars []string
	for _, a := range f.args {
		decl = append(decl, "$"+a.name+": "+a.typ.String())
		call = append(call, a.name+": $"+a.name)
		sig = append(sig, a.name+": "+a.typ.String())
		vars = append(vars, "  "+jsonString(a.name)+": "+g.placeholder(a.typ))
	}
	opName := strings.ToUpper(f.name[:1]) + f.name[1:]
	var q strings.Builder
	q.WriteString(op + " " + opName)
	if len(decl) > 0 {
		q.WriteString("(" + strings.Join(decl, ", ") + ")")
	}
	q.WriteString(" {\n  " + f.name)
	if len(call) > 0 {
		q.WriteString("(" + strings.Join(call, ", ") + ")")
	}
	q.WriteString(g.selection(f.typ, 1))
	q.WriteString("\n}\n")
	varsText := "{}"
	if len(vars) > 0 {
		varsText = "{\n" + strings.Join(vars, ",\n") + "\n}"
	}

	signature := td.name + "." + f.name
	if len(sig) > 0 {
		signature += "(" + strings.Join(sig, ", ") + ")"
	}
	signature += ": " + f.typ.String()
	desc := "`" + signature + "`"
	if f.desc != "" {
		desc += "\n\n" + f.desc
	}
	if f.deprecated {
		desc += "\n\n**Deprecated**"
		if f.reason != "" {
			desc += ": " + f.reason
		}
	}
	if !g.s.known(f.typ.base()) {
		desc += "\n\nReturn type `" + f.typ.base() + "` is not defined in the schema; no selection set was generated."
	}
	uid := b.request(folder, f.name, desc, endpoint, q.String(), varsText)
	b.reqs++
	path := folderName + "/" + f.name
	if !g.s.known(f.typ.base()) {
		b.add(impkit.NeedsReview, path, uid, "unknown-type", "return type "+f.typ.base()+" is not defined in the schema", "Add a selection set by hand")
	}
	if op == "subscription" {
		b.add(impkit.Degraded, path, uid, "subscription", "subscriptions normally run over WebSocket (graphql-transport-ws / graphql-ws) or SSE; this request is sent as a plain POST, which many servers reject", "Use a WebSocket client for real subscription testing")
	}
}

func jsonString(s string) string {
	b, err := postman.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
