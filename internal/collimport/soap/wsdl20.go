package soap

import (
	"fmt"
	"strings"
)

// wsdl20 imports a WSDL 2.0 description. Coverage is partial and reported as
// such: SOAP-bound interfaces whose operations have an input element become
// document/literal requests. HTTP bindings, rpc signatures, message exchange
// patterns without an input, #any/#other messages, features, properties and
// modules are reported, not synthesized.
func (im *importer) wsdl20(root *node) {
	c := &im.res.Collection
	c.Name = root.attr("name")
	im.ss.collectHints(root)
	im.schemasUnder(root)
	im.policyScan(root)
	im.report(Degraded, "", "wsdl2-partial", "WSDL 2.0 support is partial: SOAP-bound operations with an input element are synthesized as document/literal; HTTP bindings, rpc signatures, #any/#other messages, features/properties and modules are reported but not generated", "")

	ifaces := map[string]*node{}
	bindings := map[string]*node{}
	var bindOrder, services []*node
	for _, k := range root.kids {
		if k.space != nsWSDL20 {
			continue
		}
		switch k.local {
		case "interface":
			ifaces[k.attr("name")] = k
		case "binding":
			bindings[k.attr("name")] = k
			bindOrder = append(bindOrder, k)
		case "service":
			services = append(services, k)
		case "import", "include":
			loc := k.attr("location")
			if k.local == "import" && root.attr("targetNamespace") == k.attr("namespace") {
				continue
			}
			im.report(Degraded, loc, "external-wsdl-import", fmt.Sprintf("wsdl:%s of %q was not fetched; interfaces, bindings or types from it are missing", k.local, loc), "Merge the definitions into the document and re-import")
		}
	}
	if c.Name == "" && len(services) > 0 {
		c.Name = services[0].attr("name")
	}
	used := map[*node]bool{}
	for _, svc := range services {
		sname := svc.attr("name")
		var sf string
		for _, ep := range svc.childrenNamed(nsWSDL20, "endpoint") {
			path := sname + "/" + ep.attr("name")
			b := bindings[localName(ep.attr("binding"))]
			if b == nil {
				im.report(Blocked, path, "missing-binding", "endpoint refers to binding "+localName(ep.attr("binding"))+" which is not defined in the supplied documents", "")
				continue
			}
			used[b] = true
			if b.attr("type") != nsWSDL20SOAP {
				im.report(Unsupported, path, "non-soap-binding", "binding type "+b.attr("type")+" is not the WSDL 2.0 SOAP binding; skipped", "")
				continue
			}
			addr := strings.TrimSpace(ep.attr("address"))
			if !im.endpointOK(path, addr) {
				continue
			}
			if sf == "" {
				sf = im.folder(sname, "", "")
			}
			pf := im.folder(ep.attr("name"), sf, "Binding: "+b.attr("name"))
			im.wsdl20Binding(b, ifaces, sname, ep.attr("name"), addr, pf)
		}
	}
	for _, b := range bindOrder {
		if used[b] || b.attr("type") != nsWSDL20SOAP {
			continue
		}
		im.report(NeedsReview, "binding/"+b.attr("name"), "no-endpoint", "binding is not referenced by any service endpoint, so there is no address; requests use {{endpoint}}", "Set an endpoint variable or edit the URLs")
		f := im.folder("Binding: "+b.attr("name"), "", "")
		im.wsdl20Binding(b, ifaces, "", b.attr("name"), "{{endpoint}}", f)
	}
}

func ifaceOps(i *node, ifaces map[string]*node, seen map[*node]bool) []*node {
	if i == nil || seen[i] {
		return nil
	}
	seen[i] = true
	var ops []*node
	for _, e := range strings.Fields(i.attr("extends")) {
		ops = append(ops, ifaceOps(ifaces[localName(e)], ifaces, seen)...)
	}
	return append(ops, i.childrenNamed(nsWSDL20, "operation")...)
}

func (im *importer) wsdl20Binding(b *node, ifaces map[string]*node, svc, port, endpoint, parent string) {
	prefix := svc + "/" + port
	if svc == "" {
		prefix = port
	}
	iface := ifaces[localName(b.attr("interface"))]
	if iface == nil {
		im.report(Blocked, prefix, "missing-interface", "binding refers to interface "+localName(b.attr("interface"))+" which is not defined in the supplied documents", "")
		return
	}
	v12 := true
	if v, ok := b.attrNS(nsWSDL20SOAP, "version"); ok && strings.TrimSpace(v) == "1.1" {
		v12 = false
	}
	ver := "1.2"
	if !v12 {
		ver = "1.1"
	}
	actions := map[string]*string{}
	for _, bo := range b.childrenNamed(nsWSDL20, "operation") {
		if a, ok := bo.attrNS(nsWSDL20SOAP, "action"); ok {
			actions[bo.qname(bo.attr("ref")).local] = strp(a)
		}
	}
	for _, op := range ifaceOps(iface, ifaces, map[*node]bool{}) {
		name := op.attr("name")
		path := prefix + "/" + name
		in := op.child(nsWSDL20, "input")
		if in == nil {
			im.report(Unsupported, path, "no-input", "operation has no input message; there is nothing to send", "")
			continue
		}
		issue := func(f, m string) { im.reportOnce(path+"|"+f, Degraded, path, f, m, "") }
		g := newGen(im.ss, im.opt.MaxDepth, v12, issue)
		if _, ok := op.attrNS(nsWSDL20RPC, "signature"); ok {
			issue("wsdl2-rpc-signature", "wrpc:signature (rpc style) is not interpreted; the body uses the input element")
		}
		var body strings.Builder
		el := in.attr("element")
		switch el {
		case "#none":
		case "#any", "#other", "":
			g.line(&body, 2, comment(" input element is "+orDefault(el, "#other")+": add the message content here "))
			issue("wsdl2-any-input", "the input message is declared as "+orDefault(el, "#other")+"; an empty body with a comment was generated")
		default:
			g.emitElement(g.globalElement(in.qname(el)), 2, &body)
		}
		var hdr strings.Builder
		if im.hasPolicy {
			g.line(&hdr, 2, comment(" WS-Policy declared by this WSDL: add a wsse:Security header with your own credentials (none were generated) "))
		}
		action := actions[name]
		label := "document/literal"
		side := map[string]any{"binding": b.attr("name"), "port": port, "operation": name, "style": label, "soapVersion": ver, "wsdl": "2.0"}
		if svc != "" {
			side["service"] = svc
		}
		if action != nil {
			side["soapAction"] = *action
		}
		desc := fmt.Sprintf("SOAP %s %s (WSDL 2.0)", ver, label)
		if action != nil {
			desc += fmt.Sprintf("\n\nSOAPAction: %q", *action)
		}
		if !im.addRequest(parent, reqSpec{name: name, desc: desc, path: path, endpoint: endpoint, v12: v12, action: action,
			body: g.envelope(hdr.String(), body.String()), sidecar: side}) {
			return
		}
		im.report(Converted, path, "operation-style", fmt.Sprintf("SOAP %s %s (WSDL 2.0)", ver, label), "")
	}
}
