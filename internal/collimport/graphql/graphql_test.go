package graphql

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parse(t *testing.T, src string, opt Options) *Result {
	t.Helper()
	opt.NewID = counter()
	res, err := Parse([]byte(src), opt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, _ := json.MarshalIndent(v, "", "  ")
	got = append(got, '\n')
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s; rerun with -update and review the diff", name)
	}
}

type gqlBody struct {
	Mode    string `json:"mode"`
	GraphQL struct {
		Query     string `json:"query"`
		Variables string `json:"variables"`
	} `json:"graphql"`
}

func find(t *testing.T, r *Result, name string) (store.Item, gqlBody) {
	t.Helper()
	for _, it := range r.Items {
		if it.Kind == "request" && it.Name == name {
			var b gqlBody
			if err := json.Unmarshal(it.Body, &b); err != nil {
				t.Fatal(err)
			}
			return it, b
		}
	}
	t.Fatalf("request %q not found", name)
	return store.Item{}, gqlBody{}
}

func TestGoldenMediumSDL(t *testing.T) {
	r := parse(t, readFile(t, "medium.graphql"), Options{})
	golden(t, "medium.golden.json", r)
}

func TestGoldenIntrospection(t *testing.T) {
	r := parse(t, readFile(t, "introspection.json"), Options{})
	golden(t, "introspection.golden.json", r)
}

func TestMediumShape(t *testing.T) {
	r := parse(t, readFile(t, "medium.graphql"), Options{})
	if r.Report.Format != "graphql" || r.Kind != "collection" {
		t.Fatalf("format/kind: %s %s", r.Report.Format, r.Kind)
	}
	if len(r.Variables) != 1 || r.Variables[0].Key != "baseUrl" {
		t.Fatalf("variables: %+v", r.Variables)
	}
	folders := map[string]int{}
	var first store.Item
	for i, it := range r.Items {
		if i == 0 {
			first = it
		}
		if it.Kind == "folder" {
			folders[it.Name] = 0
		}
	}
	if first.Name != "Introspection query" || first.ParentUID != "" {
		t.Fatalf("first item: %+v", first)
	}
	byUID := map[string]string{}
	for _, it := range r.Items {
		byUID[it.UID] = it.Name
	}
	for _, it := range r.Items {
		if it.Kind == "request" && it.ParentUID != "" {
			folders[byUID[it.ParentUID]]++
		}
	}
	want := map[string]int{"Queries": 12, "Mutations": 6, "Subscriptions": 2}
	for k, v := range want {
		if folders[k] != v {
			t.Errorf("folder %s has %d requests, want %d", k, folders[k], v)
		}
	}
	it, b := find(t, r, "user")
	if it.Method != "POST" || b.Mode != "graphql" {
		t.Fatalf("user: %s %s", it.Method, b.Mode)
	}
	var u struct{ Raw string }
	_ = json.Unmarshal(it.URL, &u)
	if u.Raw != "{{baseUrl}}/graphql" {
		t.Fatalf("url: %s", it.URL)
	}
	if !json.Valid([]byte(b.GraphQL.Variables)) {
		t.Fatalf("variables not JSON: %s", b.GraphQL.Variables)
	}
	// every generated query must be brace-balanced and non-empty-selection
	for _, it := range r.Items {
		if it.Kind != "request" {
			continue
		}
		var bb gqlBody
		_ = json.Unmarshal(it.Body, &bb)
		checkWellFormed(t, it.Name, bb.GraphQL.Query)
	}
	// the report counts every level it should
	if r.Report.Counts["degraded"] < 2 {
		t.Fatalf("counts: %+v", r.Report.Counts)
	}
}

// checkWellFormed does structural checks on a generated document.
func checkWellFormed(t *testing.T, name, q string) {
	t.Helper()
	depth := 0
	for _, line := range strings.Split(q, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasSuffix(l, "{") {
			depth++
		}
		if l == "}" {
			depth--
		}
		if strings.HasSuffix(l, "{") && strings.Contains(l, "{ }") {
			t.Errorf("%s: empty selection %q", name, l)
		}
		if depth < 0 {
			t.Fatalf("%s: unbalanced braces:\n%s", name, q)
		}
	}
	if depth != 0 {
		t.Fatalf("%s: unbalanced braces:\n%s", name, q)
	}
}

func TestSelectionTable(t *testing.T) {
	tests := []struct {
		name  string
		sdl   string
		opt   Options
		field string
		query string
		vars  string
	}{
		{
			name:  "scalar return has no selection",
			sdl:   "type Query { ping: String! }",
			field: "ping",
			query: "query Ping {\n  ping\n}\n",
			vars:  "{}",
		},
		{
			name:  "nested object expansion leaves first",
			sdl:   "type Query { me: User }\ntype User { id: ID! profile: Profile name: String }\ntype Profile { bio: String avatar: Image }\ntype Image { url: String! }",
			field: "me",
			query: "query Me {\n  me {\n    id\n    name\n    profile {\n      bio\n      avatar {\n        url\n      }\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "depth limit 1 drops composites",
			sdl:   "type Query { me: User }\ntype User { id: ID! profile: Profile }\ntype Profile { bio: String }",
			opt:   Options{MaxDepth: 1},
			field: "me",
			query: "query Me {\n  me {\n    id\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "self recursion is cut",
			sdl:   "type Query { user: User }\ntype User { id: ID! friend: User parent: User boss: Boss }\ntype Boss { name: String reports: [User!]! }",
			opt:   Options{MaxDepth: 5},
			field: "user",
			query: "query User {\n  user {\n    id\n    boss {\n      name\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "mutual recursion terminates",
			sdl:   "type Query { a: A }\ntype A { x: Int b: B }\ntype B { y: Int a: A c: C }\ntype C { z: Int a: A b: B }",
			opt:   Options{MaxDepth: 5},
			field: "a",
			query: "query A {\n  a {\n    x\n    b {\n      y\n      c {\n        z\n      }\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "object with only object fields at the depth limit gets __typename",
			sdl:   "type Query { a: A }\ntype A { b: B }\ntype B { c: C }\ntype C { d: Int }",
			opt:   Options{MaxDepth: 2},
			field: "a",
			query: "query A {\n  a {\n    b {\n      __typename\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "interface uses inline fragments on concrete types",
			sdl:   "type Query { pet: Pet }\ninterface Pet { name: String! }\ntype Dog implements Pet { name: String! bark: Int }\ntype Cat implements Pet { name: String! lives: Int }",
			field: "pet",
			query: "query Pet {\n  pet {\n    __typename\n    name\n    ... on Dog {\n      bark\n    }\n    ... on Cat {\n      lives\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "union uses inline fragments",
			sdl:   "type Query { search: [R!]! }\nunion R = A | B\ntype A { a: Int }\ntype B { b: String }",
			field: "search",
			query: "query Search {\n  search {\n    __typename\n    ... on A {\n      a\n    }\n    ... on B {\n      b\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "nested field needing arguments is omitted",
			sdl:   "type Query { me: User }\ntype User { id: ID! secret(key: String!): String posts(first: Int): [Post!]! }\ntype Post { title: String }",
			field: "me",
			query: "query Me {\n  me {\n    id\n    posts {\n      title\n    }\n  }\n}\n",
			vars:  "{}",
		},
		{
			name:  "non-null and list arguments",
			sdl:   "type Query { f(a: ID!, b: [Int!]!, c: [String], d: [[Int!]!]!, e: Boolean, g: Float!, h: String!, i: Int!, j: Bool!): String }\nscalar Bool",
			field: "f",
			query: "query F($a: ID!, $b: [Int!]!, $c: [String], $d: [[Int!]!]!, $e: Boolean, $g: Float!, $h: String!, $i: Int!, $j: Bool!) {\n  f(a: $a, b: $b, c: $c, d: $d, e: $e, g: $g, h: $h, i: $i, j: $j)\n}\n",
			vars:  "{\n  \"a\": \"\",\n  \"b\": [],\n  \"c\": null,\n  \"d\": [],\n  \"e\": null,\n  \"g\": 0,\n  \"h\": \"\",\n  \"i\": 0,\n  \"j\": \"\"\n}",
		},
		{
			name:  "enum and input object arguments",
			sdl:   "type Query { f(k: Kind!, in: In!, opt: In, lk: [Kind!]!): Int }\nenum Kind { ONE TWO }\ninput In { a: Int }",
			field: "f",
			query: "query F($k: Kind!, $in: In!, $opt: In, $lk: [Kind!]!) {\n  f(k: $k, in: $in, opt: $opt, lk: $lk)\n}\n",
			vars:  "{\n  \"k\": \"ONE\",\n  \"in\": {},\n  \"opt\": null,\n  \"lk\": []\n}",
		},
		{
			name:  "legacy comma separated implements, extend, directives and default values",
			sdl:   "interface A { x: Int }\ninterface B { y: Int }\ntype Query implements A, B @key(fields: \"x\") { x: Int y(d: [Int] = [1, 2], o: In = {a: 1}): Int }\ninput In { a: Int }\nextend type Query { z: String }",
			field: "z",
			query: "query Z {\n  z\n}\n",
			vars:  "{}",
		},
		{
			name:  "unknown return type is reported not fatal",
			sdl:   "type Query { ghost: Nope }",
			field: "ghost",
			query: "query Ghost {\n  ghost\n}\n",
			vars:  "{}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(t, tc.sdl, tc.opt)
			_, b := find(t, r, tc.field)
			if b.GraphQL.Query != tc.query {
				t.Errorf("query:\n%s\nwant:\n%s", b.GraphQL.Query, tc.query)
			}
			if b.GraphQL.Variables != tc.vars {
				t.Errorf("variables:\n%s\nwant:\n%s", b.GraphQL.Variables, tc.vars)
			}
			if !json.Valid([]byte(b.GraphQL.Variables)) {
				t.Errorf("variables are not valid JSON")
			}
		})
	}
}

func TestUnknownReturnTypeNeedsReview(t *testing.T) {
	r := parse(t, "type Query { ghost: Nope }", Options{})
	if lvl(r, "unknown-type") != "needs-review" {
		t.Fatalf("report: %+v", r.Report.Entries)
	}
}

func lvl(r *Result, feature string) string {
	for _, e := range r.Report.Entries {
		if e.Feature == feature {
			return string(e.Level)
		}
	}
	return ""
}

func TestDepthCapAndClamp(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("type Query { n1: N1 }\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&sb, "type N%d { v: Int next: N%d }\n", i, i+1)
	}
	sb.WriteString("type N10 { v: Int }\n")
	r := parse(t, sb.String(), Options{MaxDepth: 99})
	_, b := find(t, r, "n1")
	if got := strings.Count(b.GraphQL.Query, "{"); got != 1+5 { // operation + five selection levels
		t.Fatalf("selection levels = %d\n%s", got-1, b.GraphQL.Query)
	}
	if lvl(r, "max-depth") != "degraded" || lvl(r, "selection-depth") != "degraded" {
		t.Fatalf("report: %+v", r.Report.Entries)
	}
	def := parse(t, sb.String(), Options{})
	_, b = find(t, def, "n1")
	if got := strings.Count(b.GraphQL.Query, "{"); got != 1+3 {
		t.Fatalf("default depth levels = %d", got-1)
	}
}

func TestSelectionBudget(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("type Query { big: Big }\ntype Big {")
	for i := 0; i < maxSelectionNodes+50; i++ {
		fmt.Fprintf(&sb, " f%d: Int", i)
	}
	sb.WriteString(" }")
	r := parse(t, sb.String(), Options{})
	_, b := find(t, r, "big")
	if got := strings.Count(b.GraphQL.Query, "\n    f"); got != maxSelectionNodes {
		t.Fatalf("selected %d fields, want %d", got, maxSelectionNodes)
	}
	if lvl(r, "selection-size") != "degraded" {
		t.Fatalf("report: %+v", r.Report.Entries)
	}
}

func TestAbstractTypeCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("type Query { u: U }\nunion U = ")
	for i := 0; i < maxAbstractTypes+5; i++ {
		if i > 0 {
			sb.WriteString(" | ")
		}
		fmt.Fprintf(&sb, "T%d", i)
	}
	sb.WriteString("\n")
	for i := 0; i < maxAbstractTypes+5; i++ {
		fmt.Fprintf(&sb, "type T%d { v: Int }\n", i)
	}
	r := parse(t, sb.String(), Options{})
	_, b := find(t, r, "u")
	if got := strings.Count(b.GraphQL.Query, "... on"); got != maxAbstractTypes {
		t.Fatalf("fragments = %d", got)
	}
	if lvl(r, "selection-types") != "degraded" {
		t.Fatalf("report: %+v", r.Report.Entries)
	}
}

func TestDeprecatedSubscriptionAndDescriptions(t *testing.T) {
	r := parse(t, readFile(t, "medium.graphql"), Options{})
	health, _ := find(t, r, "health")
	if !strings.Contains(health.DescriptionMD, "**Deprecated**") {
		t.Fatalf("deprecated note missing: %q", health.DescriptionMD)
	}
	node, _ := find(t, r, "node")
	if !strings.Contains(node.DescriptionMD, "Fetch any node by id.") || !strings.Contains(node.DescriptionMD, "`Query.node(id: ID!): Node`") {
		t.Fatalf("description: %q", node.DescriptionMD)
	}
	sub, _ := find(t, r, "orderUpdated")
	n := 0
	for _, e := range r.Report.Entries {
		if e.Feature == "subscription" {
			n++
			if e.Level != "degraded" || e.Item == "" {
				t.Fatalf("subscription entry: %+v", e)
			}
		}
	}
	if n != 2 || sub.Method != "POST" {
		t.Fatalf("subscription entries = %d", n)
	}
	// deprecated field with a reason is kept
	r2 := parse(t, readFile(t, "introspection.json"), Options{})
	owners, _ := find(t, r2, "owners")
	if !strings.Contains(owners.DescriptionMD, "Use pet.owner.") {
		t.Fatalf("reason missing: %q", owners.DescriptionMD)
	}
}

func TestIntrospectionRequestIsFirst(t *testing.T) {
	r := parse(t, "type Query { a: Int }", Options{})
	it := r.Items[0]
	var b gqlBody
	_ = json.Unmarshal(it.Body, &b)
	if it.Name != "Introspection query" || it.Method != "POST" || !strings.Contains(b.GraphQL.Query, "__schema") || b.GraphQL.Variables != "{}" {
		t.Fatalf("introspection item: %+v", it)
	}
}

func TestOptions(t *testing.T) {
	r := parse(t, "type Query { a: Int }", Options{Name: "Shop", Endpoint: "https://api.example.com/gql"})
	if r.Collection.Name != "Shop" || len(r.Variables) != 0 {
		t.Fatalf("collection/vars: %s %+v", r.Collection.Name, r.Variables)
	}
	if !strings.Contains(string(r.Items[0].URL), `"https://api.example.com/gql"`) {
		t.Fatalf("url: %s", r.Items[0].URL)
	}
	r = parse(t, "type Query { a: Int }", Options{BaseURL: "https://staging.example.com"})
	if r.Variables[0].InitialValue != "https://staging.example.com" {
		t.Fatalf("baseUrl: %+v", r.Variables)
	}
}

// ---- SDL / introspection equivalence ----

type ij = map[string]any

func refJSON(t *tref) ij {
	switch t.kind {
	case refList:
		return ij{"kind": "LIST", "name": nil, "ofType": refJSON(t.elem)}
	case refNonNull:
		return ij{"kind": "NON_NULL", "name": nil, "ofType": refJSON(t.elem)}
	}
	return ij{"kind": "X", "name": t.name, "ofType": nil}
}

func fixKinds(s *schemaDef, v ij) ij {
	if v["name"] != nil {
		k := kScalar
		if td := s.types[v["name"].(string)]; td != nil {
			k = td.kind
		}
		v["kind"] = k
	}
	if o, ok := v["ofType"].(ij); ok {
		fixKinds(s, o)
	}
	return v
}

func inputsJSON(s *schemaDef, in []inputValue) []ij {
	out := []ij{}
	for _, a := range in {
		m := ij{"name": a.name, "type": fixKinds(s, refJSON(a.typ)), "defaultValue": nil}
		if a.hasDefault {
			m["defaultValue"] = "x"
		}
		out = append(out, m)
	}
	return out
}

// toIntrospection renders a parsed schema as a standard introspection result.
func toIntrospection(s *schemaDef) []byte {
	var types []ij
	for _, n := range s.order {
		td := s.types[n]
		m := ij{"kind": td.kind, "name": td.name, "description": nil, "fields": nil, "inputFields": nil, "interfaces": nil, "enumValues": nil, "possibleTypes": nil}
		if td.desc != "" {
			m["description"] = td.desc
		}
		switch td.kind {
		case kObject, kInterface:
			fs := []ij{}
			for _, f := range td.fields {
				fm := ij{"name": f.name, "description": nil, "args": inputsJSON(s, f.args), "type": fixKinds(s, refJSON(f.typ)), "isDeprecated": f.deprecated, "deprecationReason": nil}
				if f.desc != "" {
					fm["description"] = f.desc
				}
				if f.reason != "" {
					fm["deprecationReason"] = f.reason
				}
				fs = append(fs, fm)
			}
			m["fields"] = fs
			if td.kind == kInterface {
				m["possibleTypes"] = namesJSON(s, td.possible)
			}
		case kUnion:
			m["possibleTypes"] = namesJSON(s, td.possible)
		case kInput:
			m["inputFields"] = inputsJSON(s, td.inputs)
		case kEnum:
			ev := []ij{}
			for _, e := range td.enums {
				ev = append(ev, ij{"name": e})
			}
			m["enumValues"] = ev
		}
		types = append(types, m)
	}
	root := func(n string) any {
		if n == "" {
			return nil
		}
		return ij{"name": n}
	}
	b, _ := json.Marshal(ij{"data": ij{"__schema": ij{"queryType": root(s.query), "mutationType": root(s.mutation),
		"subscriptionType": root(s.subscription), "types": types}}})
	return b
}

func namesJSON(s *schemaDef, names []string) []ij {
	out := []ij{}
	for _, n := range names {
		out = append(out, fixKinds(s, ij{"kind": "X", "name": n, "ofType": nil}))
	}
	return out
}

func TestSDLAndIntrospectionAreEquivalent(t *testing.T) {
	srcs := []string{readFile(t, "medium.graphql"),
		"type Query { pet: Pet }\ninterface Pet { name: String! }\ntype Dog implements Pet { name: String! bark: Int }\nunion U = Dog",
		"type Query { a(x: [Int!]! = [1]): Int @deprecated(reason: \"no\") }"}
	for i, src := range srcs {
		s, err := parseSDL(src)
		if err != nil {
			t.Fatal(err)
		}
		a := parse(t, src, Options{})
		b := parse(t, string(toIntrospection(s)), Options{})
		aj, _ := json.MarshalIndent(a, "", " ")
		bj, _ := json.MarshalIndent(b, "", " ")
		if !bytes.Equal(aj, bj) {
			t.Fatalf("schema %d: SDL and introspection outputs differ\n%s\n---\n%s", i, firstDiff(string(aj), string(bj)), "")
		}
	}
}

func firstDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return fmt.Sprintf("line %d:\n  sdl:   %s\n  intro: %s", i, la[i], lb[i])
		}
	}
	return "length differs"
}

func TestBareSchemaWithoutDataWrapper(t *testing.T) {
	var m ij
	if err := json.Unmarshal([]byte(readFile(t, "introspection.json")), &m); err != nil {
		t.Fatal(err)
	}
	bare, _ := json.Marshal(m["data"])
	a := parse(t, string(bare), Options{})
	b := parse(t, readFile(t, "introspection.json"), Options{})
	aj, _ := json.Marshal(a.Items)
	bj, _ := json.Marshal(b.Items)
	if !bytes.Equal(aj, bj) {
		t.Fatal("bare {\"__schema\"} differs from data-wrapped result")
	}
}

// ---- empty, malformed, wrong format, bounds ----

func TestEmptySchema(t *testing.T) {
	for name, src := range map[string]string{
		"sdl no root":              "scalar Foo",
		"sdl query without fields": "type Query",
		"introspection":            `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := parse(t, src, Options{})
			if len(r.Items) != 1 || r.Items[0].Name != "Introspection query" {
				t.Fatalf("items: %d", len(r.Items))
			}
			if lvl(r, "no-root-fields") != "needs-review" {
				t.Fatalf("report: %+v", r.Report.Entries)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrNotGraphQL},
		{"whitespace", " \n\t ", ErrNotGraphQL},
		{"plain text", "hello world", ErrNotGraphQL},
		{"html", "<html><body>x</body></html>", ErrNotGraphQL},
		{"yaml", "openapi: 3.0.0\ninfo:\n  title: x\n", ErrNotGraphQL},
		{"json without schema", `{"a":1}`, ErrNotGraphQL},
		{"json with errors only", `{"errors":[{"message":"introspection is disabled __schema"}]}`, ErrNotGraphQL},
		{"query document not a schema", "{ user { id } }", ErrNotGraphQL},
		{"schema without queryType", `{"data":{"__schema":{"types":[]}}}`, ErrNotGraphQL},
		{"truncated introspection", `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OB`, ErrSyntax},
		{"unterminated brace", "type Query { a: Int", ErrSyntax},
		{"missing type", "type Query { a: }", ErrSyntax},
		{"bad token", "type Query { a: Int }\n%%%", ErrSyntax},
		{"unterminated string", "type Query {\n \"oops\n a: Int }", ErrSyntax},
		{"unterminated block string", "\"\"\"never closed\ntype Query { a: Int }", ErrSyntax},
		{"unknown keyword mid document", "type Query { a: Int }\nquery Foo { a }", ErrSyntax},
		{"bad schema op", "schema { frob: Query }", ErrSyntax},
		{"introspection type without name ref", `{"data":{"__schema":{"queryType":{"name":"Q"},"types":[{"kind":"OBJECT","name":"Q","fields":[{"name":"a","args":[],"type":{"kind":"NON_NULL"}}]}]}}}`, ErrSyntax},
		{"too deep type", "type Query { a: " + strings.Repeat("[", 40) + "Int" + strings.Repeat("]", 40) + " }", ErrSyntax},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Parse([]byte(tc.in), Options{NewID: counter()})
			if !errors.Is(err, tc.want) || res != nil {
				t.Fatalf("err = %v, res nil = %v; want %v", err, res == nil, tc.want)
			}
		})
	}
}

func TestIntrospectionRefTooDeep(t *testing.T) {
	ref := `{"kind":"SCALAR","name":"Int"}`
	for i := 0; i < maxTypeRefDepth+4; i++ {
		ref = `{"kind":"LIST","ofType":` + ref + `}`
	}
	in := `{"data":{"__schema":{"queryType":{"name":"Q"},"types":[{"kind":"OBJECT","name":"Q","fields":[{"name":"a","args":[],"type":` + ref + `}]}]}}}`
	if _, err := Parse([]byte(in), Options{}); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("err = %v", err)
	}
}

func TestInputTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte(" "), MaxInputBytes+1)
	if _, err := Parse(big, Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	ok := append(bytes.Repeat([]byte(" "), MaxInputBytes-40), []byte("type Query { a: Int }")...)
	if _, err := Parse(ok, Options{NewID: counter()}); err != nil {
		t.Fatalf("exactly-at-limit input rejected: %v", err)
	}
}

func TestTooManyRequests(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("type Query {")
	for i := 0; i < MaxRequests+7; i++ {
		fmt.Fprintf(&sb, " f%d: Int", i)
	}
	sb.WriteString(" }")
	r := parse(t, sb.String(), Options{})
	if r.Report.Stats.Requests != MaxRequests+1 { // + introspection
		t.Fatalf("requests = %d", r.Report.Stats.Requests)
	}
	if lvl(r, "too-many-requests") != "blocked" {
		t.Fatalf("report: %v", r.Report.Counts)
	}
}

func TestTooManyTypes(t *testing.T) {
	var sb strings.Builder
	for i := 0; i <= maxTypes; i++ {
		fmt.Fprintf(&sb, "scalar S%d\n", i)
	}
	if _, err := Parse([]byte(sb.String()), Options{}); !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v", err)
	}
}

func TestValueNestingBounded(t *testing.T) {
	src := "type Query { a(x: Int = " + strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + "): Int }"
	if _, err := Parse([]byte(src), Options{}); !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v", err)
	}
}

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"introspection", readFile(t, "introspection.json"), true},
		{"sdl medium", readFile(t, "medium.graphql"), true},
		{"sdl with bom", "\xef\xbb\xbftype Query { a: Int }", true},
		{"schema block", "schema {\n query: Q\n}", true},
		{"extend type", "extend type Foo @key(fields: \"id\") {\n id: ID\n}", true},
		{"json other", `{"openapi":"3.0.0"}`, false},
		{"postman", `{"info":{"schema":"https://schema.getpostman.com"}}`, false},
		{"yaml", "openapi: 3.0.0\ninfo:\n  title: x\n", false},
		{"yaml type key", "components:\n  schemas:\n    A:\n      type: object\n", false},
		{"empty", "", false},
		{"curl", "curl https://example.com", false},
	}
	for _, tc := range tests {
		if got := Sniff([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: Sniff = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	src := readFile(t, "medium.graphql")
	a, _ := json.Marshal(parse(t, src, Options{}))
	b, _ := json.Marshal(parse(t, src, Options{}))
	if !bytes.Equal(a, b) {
		t.Fatal("output is not deterministic")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(readFile2("testdata/medium.graphql"))
	f.Add(readFile2("testdata/introspection.json"))
	f.Add("type Query { a(x: [Int!]! = [1]): A } type A { a: A b: [A!] } union U = A | B")
	f.Add(`"""x""" type`)
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 1<<16 {
			t.Skip()
		}
		res, err := Parse([]byte(in), Options{NewID: counter()})
		if err == nil && res == nil {
			t.Fatal("nil result without error")
		}
	})
}

func readFile2(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}
