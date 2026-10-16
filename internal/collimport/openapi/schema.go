package openapi

import (
	"encoding/json"
	"strings"
)

// Example generation bounds.
const (
	MaxExampleNodes = 5000
	MaxExampleDepth = 12
)

// gen derives example values from schemas. $ref cycles are cut (the cyclic
// property is omitted) and reported once; total work is capped by a node
// budget so hostile fan-out schemas cannot blow up.
type gen struct {
	root    any
	budget  int
	stack   []string
	request bool // omit readOnly properties
	onIssue func(feature, msg string)
}

func newGen(root any, request bool, onIssue func(feature, msg string)) *gen {
	return &gen{root: root, budget: MaxExampleNodes, request: request, onIssue: onIssue}
}

func (g *gen) issue(f, m string) {
	if g.onIssue != nil {
		g.onIssue(f, m)
	}
}

func (g *gen) inStack(ref string) bool {
	for _, r := range g.stack {
		if r == ref {
			return true
		}
	}
	return false
}

// example returns a value for schema s (nil when nothing sensible exists).
func (g *gen) example(s any, depth int) any {
	if g.budget <= 0 {
		g.issue("example-truncated", "example generation hit its size limit; the example is partial")
		return nil
	}
	g.budget--
	if depth > MaxExampleDepth {
		g.issue("example-depth", "schema nesting deeper than "+itoa(MaxExampleDepth)+" was cut in a generated example")
		return nil
	}
	o := asMap(s)
	if o == nil {
		return nil
	}
	if ref, ok := o.get("$ref").(string); ok {
		return g.followRef(ref, depth)
	}
	if v, ok := declared(o); ok {
		return v
	}
	if all := asSlice(o.get("allOf")); len(all) > 0 {
		return g.allOf(o, all, depth)
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if alts := asSlice(o.get(k)); len(alts) > 0 {
			return g.example(alts[0], depth+1)
		}
	}
	switch schemaType(o) {
	case "object":
		return g.object(o, depth)
	case "array":
		return g.array(o, depth)
	case "string":
		return stringSample(asString(o.get("format")))
	case "integer", "number":
		if m := o.get("minimum"); m != nil {
			if n, ok := m.(json.Number); ok {
				return n
			}
		}
		return json.Number("0")
	case "boolean":
		return true
	}
	return nil
}

func (g *gen) followRef(ref string, depth int) any {
	if g.inStack(ref) {
		g.issue("ref-cycle", "$ref cycle through "+refName(ref)+" was cut in a generated example")
		return nil
	}
	target, _, err := deref(g.root, newRefObj(ref))
	if err != nil {
		g.issue("bad-ref", "$ref "+ref+" could not be resolved: "+err.Error())
		return nil
	}
	g.stack = append(g.stack, ref)
	defer func() { g.stack = g.stack[:len(g.stack)-1] }()
	return g.example(target, depth+1)
}

func newRefObj(ref string) *omap {
	o := newOmap()
	o.set("$ref", ref)
	return o
}

// declared returns an explicitly authored example/default/const/enum value.
func declared(o *omap) (any, bool) {
	if o.has("example") {
		return o.get("example"), true
	}
	if ex := asSlice(o.get("examples")); len(ex) > 0 {
		return ex[0], true
	}
	if o.has("default") {
		return o.get("default"), true
	}
	if o.has("const") {
		return o.get("const"), true
	}
	if en := asSlice(o.get("enum")); len(en) > 0 {
		return en[0], true
	}
	return nil, false
}

func schemaType(o *omap) string {
	switch t := o.get("type").(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s := asString(x); s != "null" {
				return s
			}
		}
	}
	switch {
	case o.has("properties") || o.has("additionalProperties"):
		return "object"
	case o.has("items"):
		return "array"
	}
	return ""
}

func (g *gen) allOf(o *omap, parts []any, depth int) any {
	merged := newOmap()
	var scalar any
	for _, p := range parts {
		v := g.example(p, depth+1)
		if m := asMap(v); m != nil {
			for _, k := range m.keys {
				merged.set(k, m.m[k])
			}
		} else if v != nil {
			scalar = v
		}
	}
	if props := asMap(o.get("properties")); props != nil {
		if m := g.object(o, depth); asMap(m) != nil {
			for _, k := range asMap(m).keys {
				merged.set(k, asMap(m).m[k])
			}
		}
	}
	if len(merged.keys) == 0 && scalar != nil {
		return scalar
	}
	return merged
}

func (g *gen) object(o *omap, depth int) any {
	out := newOmap()
	props := asMap(o.get("properties"))
	if props == nil {
		return out
	}
	for _, k := range props.keys {
		ps, _, err := deref(g.root, props.m[k])
		if err == nil {
			if pm := asMap(ps); pm != nil && g.request && asBool(pm.get("readOnly")) {
				continue
			}
		}
		v := g.example(props.m[k], depth+1)
		if v == nil && !isNullable(props.m[k]) {
			continue
		}
		out.set(k, v)
	}
	return out
}

func isNullable(s any) bool {
	o := asMap(s)
	return asBool(o.get("nullable"))
}

func (g *gen) array(o *omap, depth int) any {
	out := []any{}
	if it := o.get("items"); it != nil {
		if v := g.example(it, depth+1); v != nil {
			out = append(out, v)
		}
	}
	return out
}

func stringSample(format string) any {
	switch format {
	case "date-time":
		return "2024-01-01T00:00:00Z"
	case "date":
		return "2024-01-01"
	case "time":
		return "00:00:00"
	case "email":
		return "user@example.com"
	case "uuid":
		return "00000000-0000-0000-0000-000000000000"
	case "uri", "url":
		return "https://example.com"
	case "hostname":
		return "example.com"
	case "ipv4":
		return "192.0.2.1"
	case "ipv6":
		return "2001:db8::1"
	case "byte":
		return "ZXhhbXBsZQ=="
	case "password":
		return ""
	}
	return "string"
}

func itoa(n int) string {
	b, _ := marshalNoEscape(n)
	return string(b)
}

// scalarString renders a parameter/form value: scalars as-is, arrays comma
// joined, objects as compact JSON.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, scalarString(e))
		}
		return strings.Join(parts, ",")
	case *omap:
		b, _ := marshalNoEscape(t)
		return string(b)
	}
	return asString(v)
}
