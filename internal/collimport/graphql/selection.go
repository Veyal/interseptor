package graphql

import (
	"strings"
)

// Selection-set generation limits.
const (
	DefaultDepth      = 3
	MaxDepthCap       = 5
	maxSelectionNodes = 400 // fields emitted per request
	maxAbstractTypes  = 25  // inline fragments per interface / union
)

// gen builds selection sets and variable placeholders from a schema. The
// counters aggregate across the whole import for the report.
type gen struct {
	s        *schemaDef
	maxDepth int
	budget   int

	recCut    int // fields not expanded because their type is already on the path
	depthCut  int // composite fields not expanded because of the depth limit
	argCut    int // fields omitted because they need arguments
	budgetCut int // fields omitted by the per-request node budget
	fragCut   int // implementing types omitted beyond maxAbstractTypes
}

func (g *gen) resetBudget() { g.budget = maxSelectionNodes }

func indent(n int) string { return strings.Repeat("  ", n) }

func isLeaf(td *typeDef) bool { return td == nil || td.kind == kScalar || td.kind == kEnum }

func requiresArgs(f *fieldDef) bool {
	for _, a := range f.args {
		if a.typ.nonNull() && !a.hasDefault {
			return true
		}
	}
	return false
}

func onPath(stack []string, n string) bool {
	for _, s := range stack {
		if s == n {
			return true
		}
	}
	return false
}

// selection returns " {\n...}" for a composite return type, or "" for a leaf.
// ind is the indentation level of the field that owns the selection.
func (g *gen) selection(ret *tref, ind int) string {
	td := g.s.types[ret.base()]
	if isLeaf(td) || td.kind == kInput {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(" {\n")
	g.body(&sb, td, 1, []string{td.name}, ind+1)
	sb.WriteString(indent(ind) + "}")
	return sb.String()
}

// body writes the contents of a selection set for composite type td at the
// given nesting level (1 = the root field's own selection set).
func (g *gen) body(sb *strings.Builder, td *typeDef, level int, stack []string, ind int) {
	n := 0
	switch td.kind {
	case kObject:
		n = g.fields(sb, td, level, stack, ind, nil)
	case kInterface:
		sb.WriteString(indent(ind) + "__typename\n")
		n++
		n += g.fields(sb, td, level, stack, ind, nil)
		n += g.fragments(sb, td, level, stack, ind, fieldNames(td))
	case kUnion:
		sb.WriteString(indent(ind) + "__typename\n")
		n++
		n += g.fragments(sb, td, level, stack, ind, nil)
	}
	if n == 0 {
		sb.WriteString(indent(ind) + "__typename\n")
	}
}

func fieldNames(td *typeDef) map[string]bool {
	m := make(map[string]bool, len(td.fields))
	for _, f := range td.fields {
		m[f.name] = true
	}
	return m
}

// fragments writes inline fragments on the concrete types of an abstract type.
func (g *gen) fragments(sb *strings.Builder, td *typeDef, level int, stack []string, ind int, exclude map[string]bool) int {
	n := 0
	conc := g.s.concrete(td)
	if len(conc) > maxAbstractTypes {
		g.fragCut += len(conc) - maxAbstractTypes
		conc = conc[:maxAbstractTypes]
	}
	for _, c := range conc {
		if onPath(stack, c.name) {
			g.recCut++
			continue
		}
		var inner strings.Builder
		if g.fields(&inner, c, level, append(append([]string(nil), stack...), c.name), ind+1, exclude) == 0 {
			continue
		}
		sb.WriteString(indent(ind) + "... on " + c.name + " {\n")
		sb.WriteString(inner.String())
		sb.WriteString(indent(ind) + "}\n")
		n++
	}
	return n
}

// fields writes td's fields: leaves first, then composites as depth, path
// and budget allow. It returns the number of selections written.
func (g *gen) fields(sb *strings.Builder, td *typeDef, level int, stack []string, ind int, exclude map[string]bool) int {
	n := 0
	var comps []*fieldDef
	for _, f := range td.fields {
		if strings.HasPrefix(f.name, "__") || exclude[f.name] {
			continue
		}
		if requiresArgs(f) {
			g.argCut++
			continue
		}
		bt := g.s.types[f.typ.base()]
		switch {
		case isLeaf(bt):
			if g.budget <= 0 {
				g.budgetCut++
				continue
			}
			g.budget--
			sb.WriteString(indent(ind) + f.name + "\n")
			n++
		case bt.kind == kInput:
		default:
			comps = append(comps, f)
		}
	}
	for _, f := range comps {
		bt := g.s.types[f.typ.base()]
		switch {
		case level >= g.maxDepth:
			g.depthCut++
			continue
		case onPath(stack, bt.name):
			g.recCut++
			continue
		case g.budget <= 0:
			g.budgetCut++
			continue
		}
		g.budget--
		sb.WriteString(indent(ind) + f.name + " {\n")
		g.body(sb, bt, level+1, append(append([]string(nil), stack...), bt.name), ind+1)
		sb.WriteString(indent(ind) + "}\n")
		n++
	}
	return n
}

// placeholder returns the JSON text of a type-appropriate variable value.
// Nullable types get null, non-null lists [], non-null input objects {}.
func (g *gen) placeholder(t *tref) string {
	if !t.nonNull() {
		return "null"
	}
	in := t.elem
	if in.kind == refList {
		return "[]"
	}
	switch in.name {
	case "Int", "Float":
		return "0"
	case "Boolean":
		return "false"
	case "String", "ID":
		return `""`
	}
	if td := g.s.types[in.name]; td != nil {
		switch td.kind {
		case kInput:
			return "{}"
		case kEnum:
			if len(td.enums) > 0 {
				return jsonString(td.enums[0])
			}
		}
	}
	return `""`
}
