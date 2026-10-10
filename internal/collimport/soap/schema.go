package soap

import (
	"sort"
	"strings"
)

// qn is a qualified name.
type qn struct{ ns, local string }

func (q qn) String() string {
	if q.ns == "" {
		return q.local
	}
	return "{" + q.ns + "}" + q.local
}

// schemaInfo is one xs:schema element with its form defaults.
type schemaInfo struct {
	tns      string
	elemQual bool
	attrQual bool
	n        *node
}

type decl struct {
	n   *node
	sch *schemaInfo
}

// schemaSet indexes every schema supplied in the input documents. Lookups
// never leave this set: nothing is fetched and nothing is read from disk.
type schemaSet struct {
	elems, types, groups, attrGroups, attrs map[qn]decl
	schemas                                 []*schemaInfo
	tns                                     map[string]bool
	hints                                   map[string]string // namespace -> preferred prefix
}

func newSchemaSet() *schemaSet {
	return &schemaSet{
		elems: map[qn]decl{}, types: map[qn]decl{}, groups: map[qn]decl{},
		attrGroups: map[qn]decl{}, attrs: map[qn]decl{},
		tns: map[string]bool{}, hints: map[string]string{},
	}
}

// collectHints remembers the prefixes the source documents chose, so the
// synthesized envelope reads like a hand-written one (tns, ns1, ...).
func (ss *schemaSet) collectHints(n *node) {
	var chain []*node
	for p := n; p != nil; p = p.parent {
		chain = append(chain, p)
	}
	for _, p := range chain {
		prefixes := make([]string, 0, len(p.ns))
		for k := range p.ns {
			prefixes = append(prefixes, k)
		}
		sort.Strings(prefixes)
		for _, k := range prefixes {
			if k == "" || p.ns[k] == "" {
				continue
			}
			if _, ok := ss.hints[p.ns[k]]; !ok {
				ss.hints[p.ns[k]] = k
			}
		}
	}
}

func (ss *schemaSet) addSchema(n *node) {
	if !n.is(nsXSD, "schema") {
		return
	}
	ss.collectHints(n)
	si := &schemaInfo{tns: n.attr("targetNamespace"), n: n,
		elemQual: n.attr("elementFormDefault") == "qualified",
		attrQual: n.attr("attributeFormDefault") == "qualified"}
	ss.schemas = append(ss.schemas, si)
	ss.tns[si.tns] = true
	var reg func(parent *node)
	reg = func(parent *node) {
		for _, k := range parent.kids {
			if k.space != nsXSD {
				continue
			}
			name := k.attr("name")
			q := qn{si.tns, name}
			var m map[qn]decl
			switch k.local {
			case "element":
				m = ss.elems
			case "complexType", "simpleType":
				m = ss.types
			case "group":
				m = ss.groups
			case "attributeGroup":
				m = ss.attrGroups
			case "attribute":
				m = ss.attrs
			case "redefine":
				reg(k)
				continue
			}
			if m == nil || name == "" {
				continue
			}
			if _, dup := m[q]; !dup {
				m[q] = decl{k, si}
			}
		}
	}
	reg(n)
}

// importRefs lists xs:import / xs:include / xs:redefine whose target is not
// among the supplied schemas.
type importRef struct{ kind, ns, loc string }

func (ss *schemaSet) unresolvedImports() []importRef {
	var out []importRef
	seen := map[string]bool{}
	for _, si := range ss.schemas {
		for _, k := range si.n.kids {
			if k.space != nsXSD || (k.local != "import" && k.local != "include" && k.local != "redefine") {
				continue
			}
			ns := k.attr("namespace")
			if k.local != "import" {
				ns = si.tns
			}
			loc := k.attr("schemaLocation")
			if ns == nsXSD || ns == nsXML || ns == nsSoapEnc || ns == nsXSI {
				continue // built into every processor
			}
			if ns != "" && ss.satisfies(si, k.local, ns) {
				continue
			}
			if k.local == "import" && ns == "" && loc == "" {
				continue
			}
			key := k.local + "|" + ns + "|" + loc
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, importRef{k.local, ns, loc})
		}
	}
	return out
}

// satisfies reports whether an import/include is met by the supplied
// schemas: an import by any schema of that namespace, an include or redefine
// by a different schema sharing the including schema's namespace.
func (ss *schemaSet) satisfies(from *schemaInfo, kind, ns string) bool {
	if kind == "import" {
		for _, o := range ss.schemas {
			if o.tns == ns && o != from {
				return true
			}
		}
		return false
	}
	for _, o := range ss.schemas {
		if o != from && o.tns == ns {
			return true
		}
	}
	return false
}

// builtin reports whether the QName is an XSD (or SOAP-encoding) primitive.
func builtin(q qn) bool { return q.ns == nsXSD || q.ns == nsSoapEnc }

func placeholder(local string) string {
	switch local {
	case "int", "integer", "long", "short", "byte", "unsignedInt", "unsignedLong",
		"unsignedShort", "unsignedByte", "nonNegativeInteger", "nonPositiveInteger":
		return "0"
	case "positiveInteger":
		return "1"
	case "negativeInteger":
		return "-1"
	case "decimal", "float", "double":
		return "0.0"
	case "boolean":
		return "false"
	case "dateTime":
		return "2024-01-01T00:00:00Z"
	case "date":
		return "2024-01-01"
	case "time":
		return "00:00:00"
	case "duration":
		return "P1D"
	case "gYear":
		return "2024"
	case "gYearMonth":
		return "2024-01"
	case "gMonth":
		return "--01"
	case "gDay":
		return "---01"
	case "gMonthDay":
		return "--01-01"
	case "base64Binary":
		return "AA=="
	case "hexBinary":
		return "00"
	case "anyURI":
		return "http://example.com/"
	case "language":
		return "en"
	}
	return "?"
}

func firstXSD(n *node, locals ...string) *node {
	for _, k := range n.kids {
		if k.space != nsXSD {
			continue
		}
		for _, l := range locals {
			if k.local == l {
				return k
			}
		}
	}
	return nil
}

func isXSDType(n *node) bool {
	return n != nil && n.space == nsXSD && (n.local == "complexType" || n.local == "simpleType")
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
