package soap

import (
	"fmt"
	"sort"
	"strings"
)

// wsdlCtx indexes one or more WSDL 1.1 definitions documents by local name.
type wsdlCtx struct {
	messages, portTypes, bindings map[string]*node
	bindingOrder                  []*node
	services                      []*node
	imports                       []*node
	tns                           map[string]bool
	roots                         []*node
}

func newWSDLCtx() *wsdlCtx {
	return &wsdlCtx{messages: map[string]*node{}, portTypes: map[string]*node{}, bindings: map[string]*node{}, tns: map[string]bool{}}
}

func (w *wsdlCtx) add(root *node) {
	w.roots = append(w.roots, root)
	w.tns[root.attr("targetNamespace")] = true
	for _, k := range root.kids {
		if k.space != nsWSDL11 {
			continue
		}
		name := k.attr("name")
		switch k.local {
		case "message":
			if _, ok := w.messages[name]; !ok {
				w.messages[name] = k
			}
		case "portType":
			if _, ok := w.portTypes[name]; !ok {
				w.portTypes[name] = k
			}
		case "binding":
			if _, ok := w.bindings[name]; !ok {
				w.bindings[name] = k
				w.bindingOrder = append(w.bindingOrder, k)
			}
		case "service":
			w.services = append(w.services, k)
		case "import":
			w.imports = append(w.imports, k)
		}
	}
}

func defTNS(n *node) string {
	for p := n; p != nil; p = p.parent {
		if v := p.attr("targetNamespace"); v != "" {
			return v
		}
	}
	return ""
}

func (im *importer) wsdl11(root *node) {
	c := &im.res.Collection
	c.Name = root.attr("name")
	if d := root.child(nsWSDL11, "documentation"); d != nil {
		c.Description = clipStr(strings.TrimSpace(d.text), 2000)
	}
	im.ss.collectHints(root)
	im.schemasUnder(root)
	w := newWSDLCtx()
	w.add(root)
	im.wsdlDocument(w, "")
}

// wsdlDocument reports document-level findings and generates requests for
// every service port, then for bindings that no port references.
func (im *importer) wsdlDocument(w *wsdlCtx, scope string) {
	for _, imp := range w.imports {
		if w.tns[imp.attr("namespace")] {
			continue
		}
		loc := imp.attr("location")
		if loc == "" {
			loc = imp.attr("namespace")
		}
		im.report(Degraded, loc, "external-wsdl-import", fmt.Sprintf("wsdl:import of %q (namespace %q) points outside the supplied documents; it was not fetched, and messages, port types or bindings from it are missing", imp.attr("location"), imp.attr("namespace")),
			"Merge the imported definitions into the document and re-import")
	}
	im.policyScan(w.roots...)
	if im.res.Collection.Name == "" && len(w.services) > 0 {
		im.res.Collection.Name = w.services[0].attr("name")
	}
	used := map[*node]bool{}
	for _, svc := range w.services {
		sname := svc.attr("name")
		var svcFolder string
		for _, port := range svc.childrenNamed(nsWSDL11, "port") {
			pname := port.attr("name")
			bname := localName(port.attr("binding"))
			bind := w.bindings[bname]
			path := sname + "/" + pname
			if bind == nil {
				im.report(Blocked, path, "missing-binding", "port refers to binding "+bname+" which is not defined in the supplied documents; skipped", "")
				continue
			}
			used[bind] = true
			sb, v12 := soapBinding(bind)
			if sb == nil {
				im.report(Unsupported, path, "non-soap-binding", "binding "+bname+" is not a SOAP 1.1/1.2 binding (HTTP, MIME or other); skipped", "")
				continue
			}
			var addr *node
			if v12 {
				addr = port.child(nsSoap12Bind, "address")
			} else {
				addr = port.child(nsSoap11Bind, "address")
			}
			if addr == nil {
				im.report(Unsupported, path, "no-soap-address", "port has no soap:address; skipped", "")
				continue
			}
			loc := strings.TrimSpace(addr.attr("location"))
			if !im.endpointOK(path, loc) {
				continue
			}
			if tr := sb.attr("transport"); tr != "" && !strings.Contains(tr, "/http") {
				im.report(Unsupported, path, "non-http-transport", "binding transport "+tr+" is not HTTP; skipped", "")
				continue
			}
			if svcFolder == "" {
				svcFolder = im.folder(sname, "", docOf(svc))
			}
			pf := im.folder(pname, svcFolder, "Binding: "+bname)
			im.wsdlBinding(w, bind, sb, v12, sname, pname, loc, pf)
		}
	}
	for _, b := range w.bindingOrder {
		if used[b] {
			continue
		}
		sb, v12 := soapBinding(b)
		path := "binding/" + b.attr("name")
		if sb == nil {
			im.report(Unsupported, path, "non-soap-binding", "binding is not a SOAP 1.1/1.2 binding; skipped", "")
			continue
		}
		im.report(NeedsReview, path, "no-endpoint", "binding is not referenced by any service port, so there is no endpoint; requests use {{endpoint}}", "Set an endpoint variable or edit the URLs")
		f := im.folder("Binding: "+b.attr("name"), "", "")
		im.wsdlBinding(w, b, sb, v12, "", b.attr("name"), "{{endpoint}}", f)
	}
}

func docOf(n *node) string {
	if d := n.child(nsWSDL11, "documentation"); d != nil {
		return clipStr(strings.TrimSpace(d.text), 2000)
	}
	return ""
}

// endpointOK rejects non-HTTP(S) endpoints (JMS, SMTP, TCP, ...). Values with
// no scheme at all (placeholders such as REPLACE_ME) are kept and flagged.
func (im *importer) endpointOK(path, loc string) bool {
	low := strings.ToLower(loc)
	switch {
	case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"):
		return true
	case strings.Contains(low, "://") || strings.HasPrefix(low, "jms:") || strings.HasPrefix(low, "mailto:"):
		im.report(Unsupported, path, "non-http-endpoint", "endpoint "+loc+" is not HTTP(S); skipped", "")
		return false
	}
	im.report(NeedsReview, path, "placeholder-endpoint", fmt.Sprintf("endpoint %q is not an absolute HTTP URL", loc), "Edit the request URLs")
	return true
}

func soapBinding(b *node) (*node, bool) {
	if sb := b.child(nsSoap12Bind, "binding"); sb != nil {
		return sb, true
	}
	if sb := b.child(nsSoap11Bind, "binding"); sb != nil {
		return sb, false
	}
	return nil, false
}

func (im *importer) wsdlBinding(w *wsdlCtx, bind, sb *node, v12 bool, svc, port, endpoint, parent string) {
	bname := bind.attr("name")
	pt := w.portTypes[localName(bind.attr("type"))]
	prefix := svc + "/" + port
	if svc == "" {
		prefix = port
	}
	if pt == nil {
		im.report(Blocked, prefix, "missing-porttype", "binding "+bname+" refers to portType "+localName(bind.attr("type"))+" which is not defined in the supplied documents", "")
		return
	}
	for _, bop := range bind.childrenNamed(nsWSDL11, "operation") {
		if !im.wsdlOperation(w, bind, sb, v12, bop, pt, svc, port, prefix, endpoint, parent) {
			return
		}
	}
}

type partRef struct {
	msg  *node
	part *node
}

func (im *importer) wsdlOperation(w *wsdlCtx, bind, sb *node, v12 bool, bop, pt *node, svc, port, prefix, endpoint, parent string) bool {
	name := bop.attr("name")
	path := prefix + "/" + name
	bns := sb.space
	bin := bop.child(nsWSDL11, "input")
	// portType operation (overloads are told apart by the input name)
	var pop *node
	for _, c := range pt.childrenNamed(nsWSDL11, "operation") {
		if c.attr("name") != name {
			continue
		}
		if pop == nil {
			pop = c
		}
		if bin != nil && bin.attr("name") != "" {
			if in := c.child(nsWSDL11, "input"); in != nil && in.attr("name") == bin.attr("name") {
				pop = c
				break
			}
		}
	}
	if pop == nil {
		im.report(Blocked, path, "operation-not-in-porttype", "binding operation is not declared in portType "+pt.attr("name"), "")
		return true
	}
	inp := pop.child(nsWSDL11, "input")
	if inp == nil {
		im.report(Unsupported, path, "no-input", "operation has no input message (notification or solicit-response); there is nothing to send", "")
		return true
	}
	msg := w.messages[localName(inp.attr("message"))]
	if msg == nil {
		im.report(Blocked, path, "missing-message", "input message "+localName(inp.attr("message"))+" is not defined in the supplied documents", "")
		return true
	}
	sop := bop.child(bns, "operation")
	style := sb.attr("style")
	if sop != nil && sop.attr("style") != "" {
		style = sop.attr("style")
	}
	if style == "" {
		style = "document"
	}
	var action *string
	if sop != nil && sop.hasAttr("soapAction") {
		action = strp(sop.attr("soapAction"))
	}
	var body *node
	var hdrs []*node
	if bin != nil {
		body = bin.child(bns, "body")
		hdrs = bin.childrenNamed(bns, "header")
	}
	use := "literal"
	if body != nil && body.attr("use") != "" {
		use = body.attr("use")
	}
	issue := func(f, m string) { im.reportOnce(path+"|"+f, Degraded, path, f, m, "") }
	g := newGen(im.ss, im.opt.MaxDepth, v12, issue)

	// parts carried in headers are not body parts of the same message
	hdrPart := map[string]bool{}
	for _, h := range hdrs {
		if w.messages[localName(h.attr("message"))] == msg {
			hdrPart[h.attr("part")] = true
		}
	}
	var parts []*node
	var only map[string]bool
	if body != nil && body.hasAttr("parts") {
		only = map[string]bool{}
		for _, p := range strings.Fields(body.attr("parts")) {
			only[p] = true
		}
	}
	for _, p := range msg.childrenNamed(nsWSDL11, "part") {
		if only != nil && !only[p.attr("name")] || only == nil && hdrPart[p.attr("name")] {
			continue
		}
		parts = append(parts, p)
	}

	var bodyOut strings.Builder
	label := style + "/" + use
	if style == "rpc" {
		ens := ""
		if body != nil {
			ens = body.attr("namespace")
		}
		if ens == "" {
			ens = defTNS(bop)
			im.reportOnce(path, NeedsReview, path, "rpc-missing-namespace", "soap:body has no namespace for the rpc wrapper; the definitions' targetNamespace was used", "Check the operation namespace")
		}
		encoded := use == "encoded"
		enc := ""
		if encoded {
			enc = nsSoapEnc
			if body != nil && body.attr("encodingStyle") != "" {
				enc = strings.Fields(body.attr("encodingStyle"))[0]
			}
		}
		tag := g.tag(ens, name)
		attrs := ""
		if encoded {
			attrs = ` soapenv:encodingStyle="` + escAttr(enc) + `"`
		}
		var inner strings.Builder
		for _, p := range parts {
			im.rpcPart(g, p, encoded, &inner)
		}
		if inner.Len() == 0 {
			g.line(&bodyOut, 2, "<"+tag+attrs+"/>")
		} else {
			g.line(&bodyOut, 2, "<"+tag+attrs+">")
			bodyOut.WriteString(inner.String())
			g.line(&bodyOut, 2, "</"+tag+">")
		}
	} else {
		for _, p := range parts {
			if el := p.attr("element"); el != "" {
				g.emitElement(g.globalElement(p.qname(el)), 2, &bodyOut)
				continue
			}
			if t := p.attr("type"); t != "" {
				q := p.qname(t)
				issue("document-type-part", "message part "+p.attr("name")+" uses type= in a document-style binding (not WS-I compliant); emitted as an unqualified element")
				g.emitElement(elemSpec{name: p.attr("name"), typ: &q, sch: &schemaInfo{}}, 2, &bodyOut)
			}
		}
		if len(parts) == 1 && parts[0].hasAttr("element") {
			q := parts[0].qname(parts[0].attr("element"))
			if q.local == name && g.isComplexElement(q) {
				label += " (wrapped)"
			}
		}
	}

	var hdrOut strings.Builder
	if im.hasPolicy {
		g.line(&hdrOut, 2, comment(" WS-Policy declared by this WSDL: add a wsse:Security header with your own credentials (none were generated) "))
	}
	for _, h := range hdrs {
		hm := w.messages[localName(h.attr("message"))]
		if hm == nil {
			continue
		}
		for _, p := range hm.childrenNamed(nsWSDL11, "part") {
			if p.attr("name") != h.attr("part") {
				continue
			}
			if el := p.attr("element"); el != "" {
				g.emitElement(g.globalElement(p.qname(el)), 2, &hdrOut)
			} else if t := p.attr("type"); t != "" {
				q := p.qname(t)
				g.emitElement(elemSpec{name: p.attr("name"), typ: &q, sch: &schemaInfo{}}, 2, &hdrOut)
			}
		}
	}

	ver := "1.1"
	if v12 {
		ver = "1.2"
	}
	env := g.envelope(hdrOut.String(), bodyOut.String())
	side := map[string]any{"binding": bind.attr("name"), "port": port, "operation": name, "style": label, "soapVersion": ver}
	if svc != "" {
		side["service"] = svc
	}
	if action != nil {
		side["soapAction"] = *action
	}
	desc := fmt.Sprintf("SOAP %s %s", ver, label)
	if action != nil {
		desc += fmt.Sprintf("\n\nSOAPAction: %q", *action)
	}
	if d := docOf(pop); d != "" {
		desc += "\n\n" + d
	}
	if !im.addRequest(parent, reqSpec{name: name, desc: desc, path: path, endpoint: endpoint, v12: v12, action: action, body: env, sidecar: side}) {
		return false
	}
	im.report(Converted, path, "operation-style", fmt.Sprintf("SOAP %s %s", ver, label), "")
	return true
}

func (im *importer) rpcPart(g *gen, p *node, encoded bool, out *strings.Builder) {
	if el := p.attr("element"); el != "" {
		g.emitElement(g.globalElement(p.qname(el)), 3, out)
		return
	}
	t := p.attr("type")
	if t == "" {
		return
	}
	q := p.qname(t)
	sp := elemSpec{name: p.attr("name"), typ: &q, sch: &schemaInfo{}}
	if encoded {
		sp.xsiType = &q
	}
	g.emitElement(sp, 3, out)
}

// globalElement builds the spec for a top-level element by QName.
func (g *gen) globalElement(q qn) elemSpec {
	d, ok := g.ss.elems[q]
	if !ok {
		return elemSpec{name: q.local, ns: q.ns, unresolved: q.String()}
	}
	return g.specFromDecl(d.n, d.sch)
}

// isComplexElement reports whether a global element has a complex type, the
// shape of a document/literal wrapper.
func (g *gen) isComplexElement(q qn) bool {
	d, ok := g.ss.elems[q]
	if !ok {
		return false
	}
	if d.n.child(nsXSD, "complexType") != nil {
		return true
	}
	if t := d.n.attr("type"); t != "" {
		if td, ok := g.ss.types[d.n.qname(t)]; ok {
			return td.n.local == "complexType"
		}
	}
	return false
}

// policyScan reports WS-Policy / WS-SecurityPolicy declarations. It never
// generates a security header or credentials.
func (im *importer) policyScan(roots ...*node) {
	count := 0
	sp := map[string]bool{}
	for _, r := range roots {
		r.walk(func(n *node) {
			switch n.space {
			case nsPolicy2004, nsPolicy2007:
				if n.local == "Policy" || n.local == "PolicyReference" || n.local == "UsingPolicy" {
					count++
				}
			case nsSP2005, nsSP2007:
				sp[n.local] = true
			}
		})
	}
	if count == 0 && len(sp) == 0 {
		return
	}
	im.hasPolicy = true
	names := make([]string, 0, len(sp))
	for k := range sp {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > 12 {
		names = names[:12]
	}
	msg := fmt.Sprintf("the document declares WS-Policy (%d policy element(s))", count)
	if len(names) > 0 {
		msg += "; WS-SecurityPolicy assertions: " + strings.Join(names, ", ")
	}
	msg += ". No WS-Security header or credentials were generated; each envelope carries a header comment instead."
	im.report(NeedsReview, "", "ws-security-policy", msg, "Add a wsse:Security header with credentials from the engagement scope (keep them in secret variables)")
}
