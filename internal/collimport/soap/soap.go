// Package soap imports SOAP services into the collection model: WSDL 1.1
// documents, WSDL 2.0 documents (SOAP-bound, in-out subset) and SoapUI
// projects. Each WSDL operation becomes a POST request to the service endpoint
// with a complete, well-formed SOAP envelope synthesized from the XSD (element
// tree, namespaces and prefixes, type-appropriate placeholders, one instance
// of repeated elements, first branch of a choice, attributes). SoapUI
// projects already hold concrete request envelopes; those are imported
// verbatim in preference to synthesis.
//
// Parsing is data-only and hostile-input safe: nothing is fetched, executed
// or read from disk. xsd:import / xsd:include / wsdl:import are resolved only
// among the supplied documents; anything else is reported as degraded with its
// location. Any DTD or entity declaration is refused outright (ErrDTD), so
// external entities and entity-expansion bombs cannot occur.
//
// Bounds: input at most 32 MiB (MaxInputBytes); at most 5000 operations
// (MaxOperations, extra ones are reported as blocked); XML nesting depth 200
// and 600000 elements across all documents; self-referential schema types are
// expanded at most 3 levels (Options.MaxDepth, hard cap 5); an envelope is
// capped at 20000 elements, 48 levels of nesting and 4 MiB.
package soap

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxInputBytes = 32 << 20
	MaxOperations = 5000
)

// Errors.
var (
	ErrTooLarge   = errors.New("soap: input exceeds 32 MiB")
	ErrNotSOAP    = errors.New("soap: not a WSDL 1.1, WSDL 2.0 or SoapUI project document")
	ErrMalformed  = errors.New("soap: malformed XML")
	ErrDTD        = errors.New("soap: DTD/entity declarations are refused (XXE and entity-expansion defence)")
	ErrTooComplex = errors.New("soap: XML is nested or populated beyond the importer's limits")
	ErrEmpty      = errors.New("soap: no operations found")
)

// Report types are shared with the Postman importer.
type (
	Report    = postman.Report
	Entry     = postman.Entry
	Level     = postman.Level
	EnvImport = postman.EnvImport
)

const (
	Converted      = postman.Converted
	Degraded       = postman.Degraded
	PreservedInert = postman.PreservedInert
	Unsupported    = postman.Unsupported
	Blocked        = postman.Blocked
	NeedsReview    = postman.NeedsReview
)

// Options tune a parse. NewID defaults to store.NewUID (tests inject a
// counter). MaxDepth bounds recursion of self-referential schema types
// (default 3, hard cap 5).
type Options struct {
	NewID    func() string
	MaxDepth int
}

// Result is a parsed SOAP import, ready to preview and commit.
type Result struct {
	Collection   store.Collection `json:"collection"`
	Items        []store.Item     `json:"items"`
	Variables    []store.Variable `json:"variables"`
	Environments []EnvImport      `json:"environments,omitempty"`
	Report       Report           `json:"report"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	b := store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}}
	b.Items = append(b.Items, r.Items...)
	b.Variables = append(b.Variables, r.Variables...)
	return b
}

type importer struct {
	opt    Options
	res    *Result
	ss     *schemaSet
	bd     *budget
	seen   map[string]bool
	prev   map[string]string
	hosts  map[string]int
	ops    int
	capped bool
	// hasPolicy is set when a WS-Policy declaration was found (see policyScan).
	hasPolicy bool
	// SoapUI ${...} property expansions seen in stored requests.
	suiExpansions    int
	suiExpansionPath string
}

// Parse imports a WSDL 1.1, WSDL 2.0 or SoapUI project document.
func Parse(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	im := &importer{opt: opt, res: &Result{}, ss: newSchemaSet(), bd: &budget{},
		seen: map[string]bool{}, prev: map[string]string{}, hosts: map[string]int{}}
	root, err := parseXML(data, im.bd)
	if err != nil {
		return nil, err
	}
	c := &im.res.Collection
	c.UID = opt.NewID()
	c.ScopePolicy = store.ScopePolicyBlock
	var format string
	switch {
	case root.is(nsWSDL11, "definitions"):
		format = "wsdl-1.1"
	case root.is(nsWSDL20, "description"):
		format = "wsdl-2.0"
	case root.is(nsSoapUI, "soapui-project"):
		format = "soapui"
	default:
		return nil, fmt.Errorf("%w (root element {%s}%s)", ErrNotSOAP, root.space, root.local)
	}
	im.res.Report.Format = format
	side := map[string]any{"format": format}
	switch format {
	case "wsdl-1.1":
		im.wsdl11(root)
	case "wsdl-2.0":
		im.wsdl20(root)
	default:
		im.soapui(root)
	}
	if c.Name == "" {
		c.Name = "Imported SOAP service"
	}
	c.Sidecar = impkit.MustJSON(side)
	im.finish()
	if im.res.Report.Stats.Requests == 0 {
		return im.res, ErrEmpty
	}
	return im.res, nil
}

func (im *importer) report(l Level, path, feature, msg, suggestion string) {
	im.res.Report.Entries = append(im.res.Report.Entries, Entry{Level: l, Path: path, Feature: feature, Message: msg, Suggestion: suggestion})
}

// reportOnce records an entry once per (feature, key).
func (im *importer) reportOnce(key string, l Level, path, feature, msg, suggestion string) {
	if im.seen[feature+"|"+key] {
		return
	}
	im.seen[feature+"|"+key] = true
	im.report(l, path, feature, msg, suggestion)
}

// registerSchemas adds every xs:schema under n and reports imports that cannot
// be satisfied from the supplied documents.
func (im *importer) schemasUnder(n *node) {
	n.walk(func(k *node) {
		if k.is(nsXSD, "schema") {
			im.ss.addSchema(k)
		}
	})
}

func (im *importer) reportExternalImports() {
	for _, r := range im.ss.unresolvedImports() {
		loc := r.loc
		if loc == "" {
			loc = r.ns
		}
		im.report(Degraded, loc, "external-import", fmt.Sprintf("xsd:%s of %q (namespace %q) points outside the supplied documents; it was not fetched, and types from it are shown as placeholders", r.kind, r.loc, r.ns),
			"Paste the referenced schema into the WSDL (or import it with the same targetNamespace) and re-import")
	}
}

func (im *importer) folder(name, parent, desc string) string {
	rank := store.RankBetween(im.prev[parent], "")
	im.prev[parent] = rank
	it := store.Item{UID: im.opt.NewID(), CollectionUID: im.res.Collection.UID, ParentUID: parent, Kind: "folder",
		Rank: rank, Name: impkit.Clip(name, 200), DescriptionMD: desc}
	im.res.Items = append(im.res.Items, it)
	im.res.Report.Stats.Folders++
	return it.UID
}

// reqSpec describes one request to add.
type reqSpec struct {
	name, desc, path string
	endpoint         string
	v12              bool
	action           *string // nil = binding declares none
	body             string
	sidecar          map[string]any
	headers          []impkit.Row // extra request headers (SoapUI custom headers)
}

// addRequest adds a POST request; false once the operation cap is reached.
func (im *importer) addRequest(parent string, rq reqSpec) bool {
	if im.ops >= MaxOperations {
		if !im.capped {
			im.capped = true
			im.report(Blocked, "", "too-many-operations", fmt.Sprintf("only the first %d operations were imported", MaxOperations), "Split the document")
		}
		return false
	}
	im.ops++
	rank := store.RankBetween(im.prev[parent], "")
	im.prev[parent] = rank
	it := store.Item{UID: im.opt.NewID(), CollectionUID: im.res.Collection.UID, ParentUID: parent, Kind: "request",
		Rank: rank, Name: impkit.Clip(rq.name, 200), Method: "POST", DescriptionMD: rq.desc}
	it.URL = impkit.URLObject(rq.endpoint, nil)
	var hs []impkit.Row
	if rq.v12 {
		ct := "application/soap+xml; charset=utf-8"
		if rq.action != nil && *rq.action != "" {
			ct += `; action="` + *rq.action + `"`
		}
		hs = append(hs, impkit.Row{Key: "Content-Type", Value: ct})
	} else {
		hs = append(hs, impkit.Row{Key: "Content-Type", Value: "text/xml; charset=utf-8"})
	}
	if rq.action != nil {
		hs = append(hs, impkit.Row{Key: "SOAPAction", Value: `"` + *rq.action + `"`})
	}
	for _, h := range rq.headers {
		replaced := false
		for i := range hs {
			if strings.EqualFold(hs[i].Key, h.Key) {
				hs[i].Value, replaced = h.Value, true
			}
		}
		if !replaced {
			hs = append(hs, h)
		}
	}
	impkit.ScanHeaderCredentials(&im.res.Report, rq.path, it.UID, hs)
	it.Headers = impkit.Rows(hs)
	it.Body = impkit.RawBody(rq.body, "xml")
	if rq.sidecar != nil {
		it.Sidecar = impkit.MustJSON(map[string]any{"soap": rq.sidecar})
	}
	im.res.Items = append(im.res.Items, it)
	im.res.Report.Stats.Requests++
	if h := hostOf(rq.endpoint); h != "" {
		im.hosts[h]++
	}
	return true
}

func hostOf(raw string) string {
	if strings.Contains(raw, "{{") {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return ""
}

func levelRank(l Level) int {
	switch l {
	case Unsupported:
		return 0
	case Blocked:
		return 1
	case NeedsReview:
		return 2
	case Degraded:
		return 3
	case PreservedInert:
		return 4
	}
	return 5
}

func (im *importer) finish() {
	r := &im.res.Report
	im.reportExternalImports()
	hosts := make([]string, 0, len(im.hosts))
	for h := range im.hosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		im.report(NeedsReview, h, "target-host", fmt.Sprintf("%d request(s) target %s; sends are blocked until the host is in scope", im.hosts[h], h), "Add the host to the engagement scope")
	}
	r.Counts = map[Level]int{}
	for _, e := range r.Entries {
		r.Counts[e.Level]++
	}
	sort.SliceStable(r.Entries, func(i, j int) bool { return levelRank(r.Entries[i].Level) < levelRank(r.Entries[j].Level) })
	r.Headline = fmt.Sprintf("%d folders, %d requests from %s; %d need review, %d degraded, %d unsupported",
		r.Stats.Folders, r.Stats.Requests, r.Format, r.Counts[NeedsReview], r.Counts[Degraded], r.Counts[Unsupported])
	im.res.Collection.ImportReport = impkit.MustJSON(*r)
}

func strp(s string) *string { return &s }

// localName returns the part of a possibly prefixed name after the colon.
func localName(s string) string {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[i+1:]
	}
	return s
}
