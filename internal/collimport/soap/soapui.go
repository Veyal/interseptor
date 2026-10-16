package soap

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
)

var (
	suiPasswordRe = regexp.MustCompile(`(?is)<[A-Za-z0-9_.-]*:?Password[^>]*>\s*[^<\s$][^<]*</`)
	suiScriptRe   = regexp.MustCompile(`(?i)script$`)
)

type suiIface struct {
	name, bindingName string
	v12               bool
	haveVersion       bool
	endpoint          string
	actions           map[string]*string
}

// soapui imports a SoapUI project. The project's own request envelopes are
// kept verbatim; an operation with no stored request is synthesized from the
// WSDL held in the project's definition cache when it can be parsed.
func (im *importer) soapui(root *node) {
	c := &im.res.Collection
	c.Name = root.attr("name")
	w := newWSDLCtx()
	var ifaceNodes []*node
	for _, ifn := range root.childrenNamed(nsSoapUI, "interface") {
		ifaceNodes = append(ifaceNodes, ifn)
		im.suiDefinitions(ifn, w)
	}
	im.policyScan(w.roots...)
	for _, imp := range w.imports {
		if !w.tns[imp.attr("namespace")] {
			im.report(Degraded, imp.attr("location"), "external-wsdl-import", fmt.Sprintf("wsdl:import of %q inside a definition cache was not fetched", imp.attr("location")), "")
		}
	}

	ifaces := map[string]*suiIface{}
	restCount := 0
	for _, ifn := range ifaceNodes {
		xt, _ := ifn.attrNS(nsXSI, "type")
		xt = localName(xt)
		name := ifn.attr("name")
		if xt != "" && xt != "WsdlInterface" {
			restCount++
			continue
		}
		info := &suiIface{name: name, bindingName: ifn.attr("bindingName"), actions: map[string]*string{}}
		switch ifn.attr("soapVersion") {
		case "1_2":
			info.v12, info.haveVersion = true, true
		case "1_1":
			info.haveVersion = true
		}
		if eps := ifn.child(nsSoapUI, "endpoints"); eps != nil {
			if e := eps.child(nsSoapUI, "endpoint"); e != nil {
				info.endpoint = strings.TrimSpace(e.text)
			}
		}
		ifaces[name] = info
		folder := im.folder(name, "", "SoapUI interface "+name+" (binding "+ifn.attr("bindingName")+")")
		for _, op := range ifn.childrenNamed(nsSoapUI, "operation") {
			opname := op.attr("name")
			if op.hasAttr("action") {
				info.actions[opname] = strp(op.attr("action"))
			}
			added := 0
			for _, call := range op.childrenNamed(nsSoapUI, "call") {
				ok, stop := im.suiRequest(call, info, opname, folder, name+"/"+opname+"/"+call.attr("name"), opname+" - "+orDefault(call.attr("name"), "Request"))
				if ok {
					added++
				}
				if stop {
					return
				}
			}
			if added == 0 && !im.suiSynth(w, info, opname, folder) {
				im.report(Degraded, name+"/"+opname, "soapui-operation-empty", "operation has no stored request and its WSDL could not be used to synthesize one; skipped", "")
			}
			if im.capped {
				return
			}
		}
	}
	if restCount > 0 {
		im.report(Unsupported, "", "soapui-rest-service", fmt.Sprintf("%d REST service(s)/interface(s) in the project were not imported (SOAP only)", restCount), "Export them as WADL/OpenAPI and use the OpenAPI importer")
	}
	im.suiTestSuites(root, ifaces)
	im.suiAudit(root)
}

// suiDefinitions parses an interface's cached WSDL/XSD parts into the shared
// indexes. A part that is malformed or carries a DTD is reported, never fatal.
func (im *importer) suiDefinitions(ifn *node, w *wsdlCtx) {
	cache := ifn.child(nsSoapUI, "definitionCache")
	if cache == nil {
		return
	}
	for _, part := range cache.childrenNamed(nsSoapUI, "part") {
		ct := part.child(nsSoapUI, "content")
		if ct == nil || strings.TrimSpace(ct.text) == "" {
			continue
		}
		loc := ""
		if u := part.child(nsSoapUI, "url"); u != nil {
			loc = strings.TrimSpace(u.text)
		}
		doc, err := parseXML([]byte(ct.text), im.bd)
		switch {
		case errors.Is(err, ErrDTD):
			im.report(Blocked, loc, "soapui-definition-refused", "a cached definition declares a DTD/entities and was not parsed (XXE defence)", "")
			continue
		case err != nil:
			im.report(Degraded, loc, "soapui-definition-unparsable", "a cached definition could not be parsed: "+err.Error(), "")
			continue
		}
		switch {
		case doc.is(nsWSDL11, "definitions"):
			im.ss.collectHints(doc)
			im.schemasUnder(doc)
			w.add(doc)
		case doc.is(nsXSD, "schema"):
			im.ss.addSchema(doc)
		}
	}
}

// suiRequest imports one stored request. ok reports that one was added; stop
// that the operation cap was hit.
func (im *importer) suiRequest(call *node, info *suiIface, opname, parent, path, name string) (ok, stop bool) {
	var body string
	if r := call.child(nsSoapUI, "request"); r != nil {
		body = strings.TrimSpace(r.text)
	}
	if body == "" {
		return false, false
	}
	endpoint := ""
	if e := call.child(nsSoapUI, "endpoint"); e != nil {
		endpoint = strings.TrimSpace(e.text)
	}
	if endpoint == "" {
		endpoint = info.endpoint
	}
	if endpoint == "" {
		endpoint = "{{endpoint}}"
		im.reportOnce(path, NeedsReview, path, "no-endpoint", "the request has no endpoint and the interface lists none; {{endpoint}} was used", "Set the endpoint")
	}
	v12 := info.v12
	if !info.haveVersion {
		v12 = strings.Contains(body, nsSoap12Env)
	}
	action := info.actions[opname]
	var extra []impkit.Row
	for _, st := range suiSettings(call) {
		if strings.HasSuffix(st.id, "@request-headers") {
			extra = append(extra, im.suiHeaders(st.val)...)
		}
	}
	side := map[string]any{"interface": info.name, "operation": opname, "source": "soapui", "soapVersion": map[bool]string{true: "1.2", false: "1.1"}[v12]}
	if action != nil {
		side["soapAction"] = *action
	}
	before := len(im.res.Items)
	if !im.addRequest(parent, reqSpec{name: name, desc: "Imported verbatim from the SoapUI project.", path: path, endpoint: endpoint, v12: v12, action: action, body: body, sidecar: side, headers: extra}) {
		return false, true
	}
	uid := im.res.Items[before].UID
	if strings.Contains(strings.ToUpper(body), "<!DOCTYPE") || strings.Contains(strings.ToUpper(body), "<!ENTITY") {
		im.report(NeedsReview, path, "doctype-in-request-body", "the stored request declares a DTD/entities; it is kept as text and was never parsed. Review before sending", "")
	}
	if suiPasswordRe.MatchString(body) {
		impkit.CountCredential(&im.res.Report, path, uid, "request body")
	}
	if strings.Contains(body, "${") {
		im.suiExpansions++
		if im.suiExpansionPath == "" {
			im.suiExpansionPath = path
		}
	}
	if cr := call.child(nsSoapUI, "credentials"); cr != nil {
		if u := cr.child(nsSoapUI, "username"); u != nil && strings.TrimSpace(u.text) != "" {
			im.report(NeedsReview, path, "soapui-credentials", "the request holds authentication credentials; they were not imported (value not shown)", "Create a secret variable and set auth on the request")
		}
	}
	im.report(Converted, path, "operation-style", "stored SoapUI request kept verbatim", "")
	return true, false
}

type suiSetting struct{ id, val string }

func suiSettings(call *node) []suiSetting {
	var out []suiSetting
	if s := call.child(nsSoapUI, "settings"); s != nil {
		for _, k := range s.childrenNamed(nsSoapUI, "setting") {
			id, _ := k.attrNS("", "id")
			out = append(out, suiSetting{id, k.text})
		}
	}
	return out
}

// suiHeaders reads the <xml-fragment><con:entry key= value=/></...> that
// SoapUI stores for custom request headers.
func (im *importer) suiHeaders(frag string) []impkit.Row {
	doc, err := parseXML([]byte(frag), im.bd)
	if err != nil {
		return nil
	}
	var rows []impkit.Row
	for _, e := range doc.kids {
		if e.local == "entry" && e.attr("key") != "" {
			rows = append(rows, impkit.Row{Key: e.attr("key"), Value: e.attr("value")})
		}
	}
	return rows
}

// suiSynth builds a request for an operation from the cached WSDL.
func (im *importer) suiSynth(w *wsdlCtx, info *suiIface, opname, parent string) bool {
	bn := info.bindingName
	if i := strings.IndexByte(bn, '}'); i >= 0 {
		bn = bn[i+1:]
	}
	bind := w.bindings[bn]
	if bind == nil {
		return false
	}
	sb, v12 := soapBinding(bind)
	pt := w.portTypes[localName(bind.attr("type"))]
	if sb == nil || pt == nil {
		return false
	}
	endpoint := info.endpoint
	if endpoint == "" {
		for _, svc := range w.services {
			for _, p := range svc.childrenNamed(nsWSDL11, "port") {
				if localName(p.attr("binding")) != bn {
					continue
				}
				for _, ns := range []string{nsSoap11Bind, nsSoap12Bind} {
					if a := p.child(ns, "address"); a != nil && endpoint == "" {
						endpoint = strings.TrimSpace(a.attr("location"))
					}
				}
			}
		}
	}
	if endpoint == "" {
		endpoint = "{{endpoint}}"
		im.reportOnce(info.name, NeedsReview, info.name, "no-endpoint", "no endpoint is known for interface "+info.name+"; {{endpoint}} was used", "Set the endpoint")
	}
	for _, bop := range bind.childrenNamed(nsWSDL11, "operation") {
		if bop.attr("name") == opname {
			before := im.res.Report.Stats.Requests
			im.wsdlOperation(w, bind, sb, v12, bop, pt, "", info.name, info.name, endpoint, parent)
			return im.res.Report.Stats.Requests > before
		}
	}
	return false
}

func (im *importer) suiTestSuites(root *node, ifaces map[string]*suiIface) {
	stepTypes := map[string]int{}
	for _, ts := range root.childrenNamed(nsSoapUI, "testSuite") {
		tsf := ""
		for _, tc := range ts.childrenNamed(nsSoapUI, "testCase") {
			tcf := ""
			for _, st := range tc.childrenNamed(nsSoapUI, "testStep") {
				typ := st.attr("type")
				if typ != "request" {
					stepTypes[typ]++
					continue
				}
				cfg := st.child(nsSoapUI, "config")
				if cfg == nil {
					continue
				}
				in := ""
				if e := cfg.child(nsSoapUI, "interface"); e != nil {
					in = strings.TrimSpace(e.text)
				}
				opn := ""
				if e := cfg.child(nsSoapUI, "operation"); e != nil {
					opn = strings.TrimSpace(e.text)
				}
				info := ifaces[in]
				if info == nil {
					info = &suiIface{name: in, actions: map[string]*string{}}
				}
				call := cfg.child(nsSoapUI, "request")
				if call == nil {
					continue
				}
				if tsf == "" {
					tsf = im.folder("Test suite: "+ts.attr("name"), "", "")
				}
				if tcf == "" {
					tcf = im.folder("Test case: "+tc.attr("name"), tsf, "")
				}
				path := ts.attr("name") + "/" + tc.attr("name") + "/" + st.attr("name")
				if _, stop := im.suiRequest(call, info, opn, tcf, path, orDefault(st.attr("name"), opn)); stop {
					return
				}
			}
		}
	}
	if len(stepTypes) > 0 {
		keys := make([]string, 0, len(stepTypes))
		for k := range stepTypes {
			keys = append(keys, fmt.Sprintf("%s x%d", orDefault(k, "unknown"), stepTypes[k]))
		}
		sort.Strings(keys)
		im.report(Unsupported, "", "soapui-test-step", "non-request test steps were not imported: "+strings.Join(keys, ", "), "")
	}
}

// suiAudit reports project features that are deliberately not imported.
func (im *importer) suiAudit(root *node) {
	scripts, assertions, mocks, wss := 0, 0, 0, 0
	root.walk(func(n *node) {
		if n.space != nsSoapUI {
			return
		}
		switch {
		case n.local == "mockService":
			mocks++
		case n.local == "assertion":
			assertions++
		case n.local == "wssContainer" || n.local == "wssEntry":
			wss++
		case suiScriptRe.MatchString(n.local) && strings.TrimSpace(n.text) != "":
			scripts++
		}
	})
	if scripts > 0 {
		im.report(Blocked, "", "groovy-script-not-imported", fmt.Sprintf("%d Groovy script(s) in the project were not imported and are never executed", scripts), "Port the logic to a collection script manually if needed")
	}
	if assertions > 0 {
		im.report(Unsupported, "", "soapui-assertions", fmt.Sprintf("%d SoapUI assertion(s) were not imported", assertions), "")
	}
	if mocks > 0 {
		im.report(Unsupported, "", "soapui-mock-service", fmt.Sprintf("%d mock service(s) were not imported", mocks), "")
	}
	if wss > 0 {
		im.report(NeedsReview, "", "soapui-ws-security", "the project configures WS-Security (outgoing/incoming WSS); it was not imported and no credentials were created", "Add the security header to the requests yourself")
	}
	if p := root.child(nsSoapUI, "properties"); p != nil && len(p.kids) > 0 {
		im.report(Unsupported, "", "soapui-properties", fmt.Sprintf("%d project propert(ies) were not imported as variables", len(p.kids)), "")
	}
	if im.suiExpansions > 0 {
		im.report(Degraded, im.suiExpansionPath, "soapui-property-expansion", fmt.Sprintf("%d request(s) contain ${...} SoapUI property expansions (first at this path); they are kept as literal text and will not be substituted", im.suiExpansions), "Replace them with {{variables}}")
	}
}
