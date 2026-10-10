package soap

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// Envelope synthesis limits (see the package comment).
const (
	DefaultDepth    = 3
	MaxDepthCap     = 5
	maxEnvElements  = 20000 // elements emitted per envelope
	maxEnvNesting   = 48    // structural nesting, independent of type recursion
	maxSimpleChain  = 16
	indentUnit      = "   "
	maxEnvelopeSize = 4 << 20
)

type content struct {
	attrs   []string
	inner   strings.Builder
	text    string
	hasText bool
}

type elemSpec struct {
	name, ns    string
	typ         *qn
	ict, ist    *node
	sch         *schemaInfo
	tnode       *node
	fixed, def  string
	min, max    string
	nillable    bool
	abstract    bool
	key         string
	xsiType     *qn
	encodingNS  string // soapenv:encodingStyle value for rpc wrappers
	unresolved  string
	wrapperOnly bool
}

// gen synthesizes one envelope. One gen per operation.
type gen struct {
	ss        *schemaSet
	maxDepth  int
	issue     func(feature, msg string)
	hints     map[string]string
	prefixes  map[string]string
	order     []string
	taken     map[string]bool
	stack     map[string]int
	count     int
	truncated bool
	size      int
	v12       bool
}

func newGen(ss *schemaSet, maxDepth int, v12 bool, issue func(string, string)) *gen {
	if maxDepth <= 0 {
		maxDepth = DefaultDepth
	}
	if maxDepth > MaxDepthCap {
		maxDepth = MaxDepthCap
	}
	g := &gen{ss: ss, maxDepth: maxDepth, issue: issue, hints: ss.hints, v12: v12,
		prefixes: map[string]string{}, taken: map[string]bool{}, stack: map[string]int{}}
	for _, p := range []string{"xml", "xmlns", "soapenv", "xsi", "xsd", "soapenc"} {
		g.taken[p] = true
	}
	g.prefixes[nsXML] = "xml"
	g.prefixes[nsXSI] = "xsi"
	g.prefixes[nsXSD] = "xsd"
	g.prefixes[nsSoapEnc] = "soapenc"
	g.prefixes[nsSoap11Env] = "soapenv"
	g.prefixes[nsSoap12Env] = "soapenv"
	return g
}

// prefix returns the prefix for a namespace, allocating one on first use.
func (g *gen) prefix(ns string) string {
	if ns == "" {
		return ""
	}
	if p, ok := g.prefixes[ns]; ok {
		g.markUsed(ns)
		return p
	}
	want := g.hints[ns]
	if want == "" || g.taken[want] {
		for i := len(g.order) + 1; ; i++ {
			want = "ns" + strconv.Itoa(i)
			if !g.taken[want] {
				break
			}
		}
	}
	g.taken[want] = true
	g.prefixes[ns] = want
	g.markUsed(ns)
	return want
}

func (g *gen) markUsed(ns string) {
	for _, o := range g.order {
		if o == ns {
			return
		}
	}
	g.order = append(g.order, ns)
}

func (g *gen) tag(ns, name string) string {
	if p := g.prefix(ns); p != "" {
		return p + ":" + name
	}
	return name
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func escAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;", "\t", "&#9;").Replace(s)
}

func comment(s string) string { return "<!--" + strings.ReplaceAll(s, "--", "- -") + "-->" }

func (g *gen) line(out *strings.Builder, ind int, s string) {
	g.size += ind*len(indentUnit) + len(s) + 1
	if g.size > maxEnvelopeSize {
		g.truncated = true
		return
	}
	out.WriteString(strings.Repeat(indentUnit, ind))
	out.WriteString(s)
	out.WriteByte('\n')
}

func (g *gen) enter(key string) bool {
	if g.stack[key] >= g.maxDepth {
		return false
	}
	g.stack[key]++
	return true
}

func (g *gen) leave(key string) { g.stack[key]-- }

// specFromDecl builds an element spec from an xs:element node. A ref is
// resolved to the global declaration; occurrence stays that of the ref.
func (g *gen) specFromDecl(n *node, sch *schemaInfo) elemSpec {
	sp := elemSpec{min: n.attr("minOccurs"), max: n.attr("maxOccurs")}
	d, top := n, false
	if ref := n.attr("ref"); ref != "" {
		q := n.qname(ref)
		gd, ok := g.ss.elems[q]
		if !ok {
			sp.name, sp.ns, sp.unresolved = q.local, q.ns, q.String()
			return sp
		}
		d, sch, top = gd.n, gd.sch, true
		sp.key = "E:" + q.String()
	}
	sp.name = d.attr("name")
	if top || d.parent != nil && d.parent.is(nsXSD, "schema") {
		sp.ns = sch.tns
		if sp.key == "" {
			sp.key = "E:" + qn{sch.tns, sp.name}.String()
		}
	} else if f := d.attr("form"); f == "qualified" || f == "" && sch.elemQual {
		sp.ns = sch.tns
	}
	sp.sch, sp.tnode = sch, d
	if t := d.attr("type"); t != "" {
		q := d.qname(t)
		sp.typ = &q
	}
	sp.ict = d.child(nsXSD, "complexType")
	sp.ist = d.child(nsXSD, "simpleType")
	sp.fixed, sp.def = d.attr("fixed"), d.attr("default")
	sp.nillable = d.attr("nillable") == "true"
	sp.abstract = d.attr("abstract") == "true"
	return sp
}

func occurrence(sp elemSpec) string {
	var parts []string
	min := sp.min == "0"
	maxN := 1
	if sp.max == "unbounded" {
		maxN = 1 << 30
	} else if v, err := strconv.Atoi(sp.max); err == nil {
		maxN = v
	}
	switch {
	case min && maxN > 1:
		parts = append(parts, "Zero or more repetitions")
	case min:
		parts = append(parts, "Optional")
	case maxN > 1:
		parts = append(parts, "1 or more repetitions")
	}
	if sp.nillable {
		parts = append(parts, "nillable")
	}
	if sp.abstract {
		parts = append(parts, "abstract element: substitute a concrete one")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") + ":"
}

// emitElement writes one instance of an element (never more, whatever
// maxOccurs says).
func (g *gen) emitElement(sp elemSpec, ind int, out *strings.Builder) {
	if sp.max == "0" || g.truncated {
		return
	}
	if g.count >= maxEnvElements || ind > maxEnvNesting {
		if !g.truncated {
			g.truncated = true
			g.issue("envelope-truncated", "the envelope was cut at the element/size limit; deeper or later parts of the schema were not expanded")
		}
		return
	}
	g.count++
	if c := occurrence(sp); c != "" {
		g.line(out, ind, comment(c))
	}
	tag := g.tag(sp.ns, sp.name)
	if sp.unresolved != "" {
		g.issue("unresolved-reference", "element "+sp.unresolved+" is not defined in the supplied documents; emitted with a placeholder")
		g.line(out, ind, "<"+tag+">?</"+tag+">")
		return
	}
	if sp.key != "" {
		if !g.enter(sp.key) {
			g.recursionStop(sp, tag, ind, out)
			return
		}
		defer g.leave(sp.key)
	}
	var c content
	g.contentFor(sp, ind+1, &c)
	attrs := strings.Join(append(g.extraAttrs(sp), c.attrs...), " ")
	if attrs != "" {
		attrs = " " + attrs
	}
	inner := c.inner.String()
	switch {
	case inner != "":
		g.line(out, ind, "<"+tag+attrs+">")
		out.WriteString(inner)
		g.line(out, ind, "</"+tag+">")
	case c.hasText:
		text := c.text
		if sp.fixed != "" {
			text = sp.fixed
		} else if sp.def != "" {
			text = sp.def
		}
		g.line(out, ind, "<"+tag+attrs+">"+esc(text)+"</"+tag+">")
	default:
		g.line(out, ind, "<"+tag+attrs+"/>")
	}
}

func (g *gen) extraAttrs(sp elemSpec) []string {
	var a []string
	if sp.encodingNS != "" {
		a = append(a, `soapenv:encodingStyle="`+escAttr(sp.encodingNS)+`"`)
	}
	if sp.xsiType != nil {
		g.prefix(nsXSI) // declare xsi on the envelope
		a = append(a, `xsi:type="`+g.prefix(sp.xsiType.ns)+`:`+escAttr(sp.xsiType.local)+`"`)
	}
	return a
}

func (g *gen) recursionStop(sp elemSpec, tag string, ind int, out *strings.Builder) {
	g.issue("recursion-limit", "a self-referential type was expanded "+strconv.Itoa(g.maxDepth)+" levels deep and then cut ("+sp.key+")")
	g.line(out, ind, "<"+tag+">"+comment(" recursion limit "+strconv.Itoa(g.maxDepth)+" reached for "+strings.TrimLeft(sp.key, "ET:")+"; not expanded ")+"</"+tag+">")
}

// contentFor fills the content of an element from its inline or named type.
func (g *gen) contentFor(sp elemSpec, ind int, c *content) {
	switch {
	case sp.ict != nil:
		g.complexContent(sp.ict, sp.sch, ind, c)
	case sp.ist != nil:
		c.text, c.hasText = g.simpleTypeValue(sp.ist, sp.sch, 0), true
	case sp.typ != nil:
		g.typeContent(*sp.typ, ind, c)
	default:
		c.text, c.hasText = "?", true // xs:anyType
	}
}

func (g *gen) typeContent(q qn, ind int, c *content) {
	if builtin(q) {
		c.text, c.hasText = placeholder(q.local), true
		return
	}
	d, ok := g.ss.types[q]
	if !ok {
		g.issue("unresolved-reference", "type "+q.String()+" is not defined in the supplied documents; a placeholder was used")
		c.text, c.hasText = "?", true
		return
	}
	if d.n.local == "simpleType" {
		c.text, c.hasText = g.simpleTypeValue(d.n, d.sch, 0), true
		return
	}
	key := "T:" + q.String()
	if !g.enter(key) {
		g.issue("recursion-limit", "a self-referential type was expanded "+strconv.Itoa(g.maxDepth)+" levels deep and then cut ("+q.String()+")")
		c.inner.WriteString(strings.Repeat(indentUnit, ind) + comment(" recursion limit reached for "+q.String()+"; not expanded ") + "\n")
		return
	}
	defer g.leave(key)
	g.complexContent(d.n, d.sch, ind, c)
}

func (g *gen) complexContent(ct *node, sch *schemaInfo, ind int, c *content) {
	if ct.attr("abstract") == "true" {
		g.issue("abstract-type", "abstract complex type "+ct.attr("name")+" cannot be sent as is; substitute a derived type with xsi:type")
	}
	g.members(ct, sch, ind, c)
	for _, k := range ct.kids {
		if k.space != nsXSD {
			continue
		}
		switch k.local {
		case "simpleContent", "complexContent":
			if d := firstXSD(k, "extension", "restriction"); d != nil {
				g.derivation(d, k.local == "simpleContent", sch, ind, c)
			}
		}
	}
}

// members handles particles and attributes that are direct children of a
// complexType, extension or restriction.
func (g *gen) members(p *node, sch *schemaInfo, ind int, c *content) {
	for _, k := range p.kids {
		if k.space != nsXSD {
			continue
		}
		switch k.local {
		case "sequence", "choice", "all", "group":
			g.particle(k, sch, ind, &c.inner)
		case "attribute":
			g.attribute(k, sch, c)
		case "attributeGroup":
			g.attrGroup(k, sch, c, 0)
		}
	}
}

func (g *gen) derivation(d *node, simple bool, sch *schemaInfo, ind int, c *content) {
	base := d.qname(d.attr("base"))
	ext := d.local == "extension"
	if simple {
		if builtin(base) {
			c.text, c.hasText = placeholder(base.local), true
		} else if bd, ok := g.ss.types[base]; ok && bd.n.local == "simpleType" {
			c.text, c.hasText = g.simpleTypeValue(bd.n, bd.sch, 0), true
		} else if ok {
			key := "T:" + base.String()
			if g.enter(key) {
				g.complexContent(bd.n, bd.sch, ind, c)
				g.leave(key)
			}
		} else {
			g.issue("unresolved-reference", "type "+base.String()+" is not defined in the supplied documents; a placeholder was used")
			c.text, c.hasText = "?", true
		}
		if en := d.child(nsXSD, "enumeration"); en != nil && !ext {
			c.text, c.hasText = en.attr("value"), true
		}
		g.members(d, sch, ind, c)
		return
	}
	if base.ns == nsSoapEnc && base.local == "Array" {
		g.soapArray(d, ind, c)
		g.members(d, sch, ind, c)
		return
	}
	if ext {
		if bd, ok := g.ss.types[base]; ok && bd.n.local == "complexType" {
			key := "T:" + base.String()
			if g.enter(key) {
				g.complexContent(bd.n, bd.sch, ind, c)
				g.leave(key)
			}
		} else if !builtin(base) {
			g.issue("unresolved-reference", "base type "+base.String()+" is not defined in the supplied documents")
		}
	}
	g.members(d, sch, ind, c)
}

// soapArray handles <restriction base="soapenc:Array"> with
// wsdl:arrayType="T[]": one <item> of type T.
func (g *gen) soapArray(d *node, ind int, c *content) {
	g.issue("soap-encoded-array", "SOAP-encoded array: one <item> element was emitted; encoded arrays are not validated")
	for _, k := range d.kids {
		if k.is(nsXSD, "attribute") {
			if v, ok := k.attrNS(nsWSDL11, "arrayType"); ok {
				v = strings.TrimSpace(v)
				if i := strings.IndexByte(v, '['); i >= 0 {
					v = v[:i]
				}
				q := k.qname(v)
				g.emitElement(elemSpec{name: "item", typ: &q, min: "0", max: "unbounded", sch: &schemaInfo{}}, ind, &c.inner)
				return
			}
		}
	}
}

func (g *gen) attrGroup(k *node, sch *schemaInfo, c *content, depth int) {
	n := k
	if ref := k.attr("ref"); ref != "" {
		gd, ok := g.ss.attrGroups[k.qname(ref)]
		if !ok || depth > maxSimpleChain {
			g.issue("unresolved-reference", "attributeGroup "+ref+" is not defined in the supplied documents")
			return
		}
		n, sch = gd.n, gd.sch
	}
	for _, a := range n.kids {
		if a.is(nsXSD, "attribute") {
			g.attribute(a, sch, c)
		} else if a.is(nsXSD, "attributeGroup") {
			g.attrGroup(a, sch, c, depth+1)
		}
	}
}

func (g *gen) attribute(k *node, sch *schemaInfo, c *content) {
	if k.attr("use") == "prohibited" {
		return
	}
	d, qualified, ns := k, false, ""
	name := k.attr("name")
	if ref := k.attr("ref"); ref != "" {
		q := k.qname(ref)
		if q.ns == nsSoapEnc {
			return // soapenc:arrayType and friends are handled structurally
		}
		name, ns, qualified = q.local, q.ns, true
		if gd, ok := g.ss.attrs[q]; ok {
			d = gd.n
			sch = gd.sch
		} else if q.ns != nsXML {
			g.issue("unresolved-reference", "attribute "+q.String()+" is not defined in the supplied documents")
		}
	} else if f := k.attr("form"); f == "qualified" || f == "" && sch.attrQual {
		qualified, ns = true, sch.tns
	}
	if name == "" {
		return
	}
	val := d.attr("fixed")
	if val == "" {
		val = d.attr("default")
	}
	if val == "" {
		switch {
		case d.attr("type") != "":
			val = g.simpleByQN(d.qname(d.attr("type")), 0)
		case d.child(nsXSD, "simpleType") != nil:
			val = g.simpleTypeValue(d.child(nsXSD, "simpleType"), sch, 0)
		default:
			val = "?"
		}
	}
	an := name
	if qualified && ns != "" {
		an = g.prefix(ns) + ":" + name
	}
	c.attrs = append(c.attrs, an+`="`+escAttr(val)+`"`)
}

func (g *gen) simpleByQN(q qn, depth int) string {
	if builtin(q) {
		return placeholder(q.local)
	}
	if d, ok := g.ss.types[q]; ok && d.n.local == "simpleType" && depth < maxSimpleChain {
		return g.simpleTypeValue(d.n, d.sch, depth+1)
	}
	if _, ok := g.ss.types[q]; !ok {
		g.issue("unresolved-reference", "type "+q.String()+" is not defined in the supplied documents; a placeholder was used")
	}
	return "?"
}

func (g *gen) simpleTypeValue(st *node, sch *schemaInfo, depth int) string {
	if depth > maxSimpleChain {
		return "?"
	}
	if r := st.child(nsXSD, "restriction"); r != nil {
		if en := r.child(nsXSD, "enumeration"); en != nil {
			return en.attr("value")
		}
		if f := r.child(nsXSD, "minInclusive"); f != nil {
			return f.attr("value")
		}
		if f := r.child(nsXSD, "minExclusive"); f != nil {
			if v, err := strconv.ParseInt(f.attr("value"), 10, 64); err == nil {
				return strconv.FormatInt(v+1, 10)
			}
			return f.attr("value")
		}
		if b := r.attr("base"); b != "" {
			return g.simpleByQN(r.qname(b), depth)
		}
		if in := r.child(nsXSD, "simpleType"); in != nil {
			return g.simpleTypeValue(in, sch, depth+1)
		}
		return "?"
	}
	if l := st.child(nsXSD, "list"); l != nil {
		if it := l.attr("itemType"); it != "" {
			return g.simpleByQN(l.qname(it), depth)
		}
		if in := l.child(nsXSD, "simpleType"); in != nil {
			return g.simpleTypeValue(in, sch, depth+1)
		}
		return "?"
	}
	if u := st.child(nsXSD, "union"); u != nil {
		if mt := strings.Fields(u.attr("memberTypes")); len(mt) > 0 {
			return g.simpleByQN(u.qname(mt[0]), depth)
		}
		if in := u.child(nsXSD, "simpleType"); in != nil {
			return g.simpleTypeValue(in, sch, depth+1)
		}
	}
	return "?"
}

// particle emits sequence/all/choice/group/element/any content.
func (g *gen) particle(n *node, sch *schemaInfo, ind int, out *strings.Builder) {
	if n.attr("maxOccurs") == "0" {
		return
	}
	switch n.local {
	case "sequence", "all":
		for _, k := range n.kids {
			if k.space == nsXSD {
				g.particle(k, sch, ind, out)
			}
		}
	case "choice":
		var alts []*node
		for _, k := range n.kids {
			if k.space == nsXSD && k.local != "annotation" {
				alts = append(alts, k)
			}
		}
		if len(alts) == 0 {
			return
		}
		if len(alts) > 1 {
			names := make([]string, len(alts))
			for i, a := range alts {
				names[i] = describeAlt(a)
			}
			g.line(out, ind, comment("You have a CHOICE of the next "+strconv.Itoa(len(alts))+" items at this level; first branch emitted: "+strings.Join(names, " | ")))
			g.issue("choice-first-branch", "a choice was resolved to its first branch; alternatives: "+strings.Join(names, ", "))
		}
		if n.attr("minOccurs") == "0" {
			g.line(out, ind, comment("Optional:"))
		}
		g.particle(alts[0], sch, ind, out)
	case "group":
		ref := n.attr("ref")
		if ref == "" {
			return
		}
		q := n.qname(ref)
		gd, ok := g.ss.groups[q]
		if !ok {
			g.issue("unresolved-reference", "group "+q.String()+" is not defined in the supplied documents")
			return
		}
		key := "G:" + q.String()
		if !g.enter(key) {
			return
		}
		defer g.leave(key)
		for _, k := range gd.n.kids {
			if k.space == nsXSD && (k.local == "sequence" || k.local == "choice" || k.local == "all") {
				g.particle(k, gd.sch, ind, out)
			}
		}
	case "element":
		g.emitElement(g.specFromDecl(n, sch), ind, out)
	case "any":
		g.line(out, ind, comment("xs:any (namespace="+orDefault(n.attr("namespace"), "##any")+"): add any element here"))
		g.issue("xs-any", "xs:any wildcard content cannot be synthesized; a comment marks the spot")
	}
}

func describeAlt(a *node) string {
	switch a.local {
	case "element":
		if n := a.attr("name"); n != "" {
			return n
		}
		if r := a.attr("ref"); r != "" {
			return r
		}
	case "group":
		return "group " + a.attr("ref")
	}
	return "(" + a.local + ")"
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// envelope assembles the final document. body and header are pre-rendered at
// indentation level 2; namespace declarations come from the prefixes used.
func (g *gen) envelope(header, body string) string {
	env := nsSoap11Env
	if g.v12 {
		env = nsSoap12Env
	}
	var b strings.Builder
	b.WriteString(`<soapenv:Envelope xmlns:soapenv="` + env + `"`)
	for _, ns := range g.order {
		if ns == nsXML || ns == nsSoap11Env || ns == nsSoap12Env {
			continue
		}
		b.WriteString(` xmlns:` + g.prefixes[ns] + `="` + escAttr(ns) + `"`)
	}
	b.WriteString(">\n")
	if strings.TrimSpace(header) == "" {
		b.WriteString(indentUnit + "<soapenv:Header/>\n")
	} else {
		b.WriteString(indentUnit + "<soapenv:Header>\n" + header + indentUnit + "</soapenv:Header>\n")
	}
	b.WriteString(indentUnit + "<soapenv:Body>\n" + body + indentUnit + "</soapenv:Body>\n")
	b.WriteString("</soapenv:Envelope>\n")
	return b.String()
}
