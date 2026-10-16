package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Type kinds, spelled as in the introspection __TypeKind enum.
const (
	kScalar    = "SCALAR"
	kObject    = "OBJECT"
	kInterface = "INTERFACE"
	kUnion     = "UNION"
	kEnum      = "ENUM"
	kInput     = "INPUT_OBJECT"
)

const (
	maxTypeRefDepth = 16
	maxTypes        = 50000
	maxFieldsTotal  = 500000
)

type refKind uint8

const (
	refNamed refKind = iota
	refList
	refNonNull
)

// tref is a type reference: a name, optionally wrapped in list / non-null.
type tref struct {
	kind refKind
	name string
	elem *tref
}

func (t *tref) String() string {
	switch t.kind {
	case refList:
		return "[" + t.elem.String() + "]"
	case refNonNull:
		return t.elem.String() + "!"
	}
	return t.name
}

func (t *tref) base() string {
	for t.kind != refNamed {
		t = t.elem
	}
	return t.name
}

func (t *tref) nonNull() bool { return t.kind == refNonNull }

type inputValue struct {
	name       string
	typ        *tref
	hasDefault bool
}

type fieldDef struct {
	name       string
	desc       string
	args       []inputValue
	typ        *tref
	deprecated bool
	reason     string
}

type typeDef struct {
	name     string
	kind     string
	desc     string
	fields   []*fieldDef
	inputs   []inputValue
	enums    []string
	ifaces   []string // declared interfaces (SDL)
	possible []string // union members / interface implementors
}

type schemaDef struct {
	types                         map[string]*typeDef
	order                         []string
	query, mutation, subscription string
	totalFields                   int
}

func newSchema() *schemaDef { return &schemaDef{types: map[string]*typeDef{}} }

func (s *schemaDef) get(name, kind string) *typeDef {
	if td, ok := s.types[name]; ok {
		return td
	}
	td := &typeDef{name: name, kind: kind}
	s.types[name] = td
	s.order = append(s.order, name)
	return td
}

// finishSDL fills implementors / union members and default root names.
func (s *schemaDef) finishSDL() {
	for _, n := range s.order {
		td := s.types[n]
		if td.kind != kObject {
			continue
		}
		for _, i := range td.ifaces {
			if it := s.types[i]; it != nil && it.kind == kInterface {
				it.possible = append(it.possible, n)
			}
		}
	}
	s.defaultRoots()
}

func (s *schemaDef) defaultRoots() {
	if s.query == "" && s.types["Query"] != nil {
		s.query = "Query"
	}
	if s.mutation == "" && s.types["Mutation"] != nil {
		s.mutation = "Mutation"
	}
	if s.subscription == "" && s.types["Subscription"] != nil {
		s.subscription = "Subscription"
	}
}

// known reports whether a type name is defined or one of the built-in scalars.
func (s *schemaDef) known(name string) bool {
	switch name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	}
	return s.types[name] != nil
}

// concrete returns the object types an abstract type can resolve to.
func (s *schemaDef) concrete(td *typeDef) []*typeDef {
	var out []*typeDef
	seen := map[string]bool{}
	for _, n := range td.possible {
		if c := s.types[n]; c != nil && c.kind == kObject && !seen[n] {
			seen[n] = true
			out = append(out, c)
		}
	}
	return out
}

// ---- introspection JSON -------------------------------------------------

type jRef struct {
	Kind   string  `json:"kind"`
	Name   *string `json:"name"`
	OfType *jRef   `json:"ofType"`
}

type jInput struct {
	Name         string  `json:"name"`
	Type         *jRef   `json:"type"`
	DefaultValue *string `json:"defaultValue"`
}

type jField struct {
	Name              string   `json:"name"`
	Description       *string  `json:"description"`
	Args              []jInput `json:"args"`
	Type              *jRef    `json:"type"`
	IsDeprecated      bool     `json:"isDeprecated"`
	DeprecationReason *string  `json:"deprecationReason"`
}

type jType struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Fields      []jField `json:"fields"`
	InputFields []jInput `json:"inputFields"`
	Interfaces  []jRef   `json:"interfaces"`
	EnumValues  []struct {
		Name string `json:"name"`
	} `json:"enumValues"`
	PossibleTypes []jRef `json:"possibleTypes"`
}

type jRoot struct {
	Name *string `json:"name"`
}

type jSchema struct {
	QueryType        *jRoot  `json:"queryType"`
	MutationType     *jRoot  `json:"mutationType"`
	SubscriptionType *jRoot  `json:"subscriptionType"`
	Types            []jType `json:"types"`
}

func (r *jRef) convert(depth int) (*tref, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: type reference is missing", ErrSyntax)
	}
	if depth > maxTypeRefDepth {
		return nil, fmt.Errorf("%w: type reference nested deeper than %d", ErrTooComplex, maxTypeRefDepth)
	}
	switch r.Kind {
	case "LIST", "NON_NULL":
		in, err := r.OfType.convert(depth + 1)
		if err != nil {
			return nil, err
		}
		if r.Kind == "LIST" {
			return &tref{kind: refList, elem: in}, nil
		}
		return &tref{kind: refNonNull, elem: in}, nil
	}
	if r.Name == nil || *r.Name == "" {
		return nil, fmt.Errorf("%w: named type reference without a name", ErrSyntax)
	}
	return &tref{kind: refNamed, name: *r.Name}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (in jInput) convert() (inputValue, error) {
	t, err := in.Type.convert(0)
	if err != nil {
		return inputValue{}, err
	}
	return inputValue{name: in.Name, typ: t, hasDefault: in.DefaultValue != nil}, nil
}

// decodeIntrospection reads a standard introspection result: either the full
// response ({"data":{"__schema":...}}) or the bare {"__schema":...} object.
func decodeIntrospection(data []byte) (*schemaDef, error) {
	var root struct {
		Data *struct {
			Schema *jSchema `json:"__schema"`
		} `json:"data"`
		Schema *jSchema        `json:"__schema"`
		Errors json.RawMessage `json:"errors"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		if bytes.Contains(data, []byte("__schema")) {
			return nil, fmt.Errorf("%w: invalid introspection JSON: %v", ErrSyntax, err)
		}
		return nil, fmt.Errorf("%w: not an introspection result", ErrNotGraphQL)
	}
	js := root.Schema
	if js == nil && root.Data != nil {
		js = root.Data.Schema
	}
	if js == nil {
		if len(root.Errors) > 0 && bytes.Contains(data, []byte("__schema")) {
			return nil, fmt.Errorf("%w: the introspection response carries errors and no schema (introspection disabled?)", ErrNotGraphQL)
		}
		return nil, fmt.Errorf("%w: no data.__schema in JSON", ErrNotGraphQL)
	}
	if js.QueryType == nil || deref(js.QueryType.Name) == "" {
		return nil, fmt.Errorf("%w: __schema has no queryType", ErrNotGraphQL)
	}
	if len(js.Types) > maxTypes {
		return nil, fmt.Errorf("%w: more than %d types", ErrTooComplex, maxTypes)
	}
	s := newSchema()
	s.query = deref(js.QueryType.Name)
	if js.MutationType != nil {
		s.mutation = deref(js.MutationType.Name)
	}
	if js.SubscriptionType != nil {
		s.subscription = deref(js.SubscriptionType.Name)
	}
	for _, jt := range js.Types {
		if jt.Name == "" {
			return nil, fmt.Errorf("%w: a type has no name", ErrSyntax)
		}
		if strings.HasPrefix(jt.Name, "__") {
			continue
		}
		td := s.get(jt.Name, jt.Kind)
		td.kind, td.desc = jt.Kind, clip(deref(jt.Description))
		s.totalFields += len(jt.Fields) + len(jt.InputFields)
		if s.totalFields > maxFieldsTotal {
			return nil, fmt.Errorf("%w: more than %d fields", ErrTooComplex, maxFieldsTotal)
		}
		for _, jf := range jt.Fields {
			f := &fieldDef{name: jf.Name, desc: clip(deref(jf.Description)), deprecated: jf.IsDeprecated, reason: deref(jf.DeprecationReason)}
			var err error
			if f.typ, err = jf.Type.convert(0); err != nil {
				return nil, err
			}
			for _, ja := range jf.Args {
				a, err := ja.convert()
				if err != nil {
					return nil, err
				}
				f.args = append(f.args, a)
			}
			td.fields = append(td.fields, f)
		}
		for _, ji := range jt.InputFields {
			a, err := ji.convert()
			if err != nil {
				return nil, err
			}
			td.inputs = append(td.inputs, a)
		}
		for _, ev := range jt.EnumValues {
			td.enums = append(td.enums, ev.Name)
		}
		for i := range jt.PossibleTypes {
			if n := jt.PossibleTypes[i].Name; n != nil {
				td.possible = append(td.possible, *n)
			}
		}
	}
	s.defaultRoots()
	return s, nil
}

func clip(s string) string {
	if len(s) > 2000 {
		s = s[:2000]
		for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
			s = s[:len(s)-1]
		}
	}
	return s
}
