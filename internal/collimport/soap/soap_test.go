package soap

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func idGen() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parseBytes(t *testing.T, data []byte, opt Options) (*Result, error) {
	t.Helper()
	opt.NewID = idGen()
	return Parse(data, opt)
}

func parseFile(t *testing.T, name string) *Result {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := parseBytes(t, data, Options{})
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return res
}

func golden(t *testing.T, name string) {
	t.Helper()
	res := parseFile(t, name)
	got, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	g := "testdata/" + strings.TrimSuffix(strings.TrimSuffix(name, ".wsdl"), ".xml") + ".golden.json"
	if *update {
		if err := os.WriteFile(g, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(g)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", g, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s; rerun with -update and review the diff", name)
	}
}

func TestGoldenBankWSDL(t *testing.T)      { golden(t, "bank.wsdl") }
func TestGoldenRPCWSDL(t *testing.T)       { golden(t, "rpc.wsdl") }
func TestGoldenWSDL2(t *testing.T)         { golden(t, "service2.wsdl") }
func TestGoldenSoapUIProject(t *testing.T) { golden(t, "soapui.xml") }

func byName(res *Result) map[string][]store.Item {
	m := map[string][]store.Item{}
	for _, it := range res.Items {
		if it.Kind == "request" {
			m[it.Name] = append(m[it.Name], it)
		}
	}
	return m
}

type hdr struct{ Key, Value string }

func headers(t *testing.T, it store.Item) map[string]string {
	t.Helper()
	var rows []hdr
	if err := json.Unmarshal(it.Headers, &rows); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

func bodyOf(t *testing.T, it store.Item) string {
	t.Helper()
	var b struct{ Raw string }
	if err := json.Unmarshal(it.Body, &b); err != nil {
		t.Fatal(err)
	}
	return b.Raw
}

func urlRaw(t *testing.T, it store.Item) string {
	t.Helper()
	var u struct{ Raw string }
	if err := json.Unmarshal(it.URL, &u); err != nil {
		t.Fatal(err)
	}
	return u.Raw
}

func has(res *Result, l Level, feature string) bool { return res.Report.Has(l, feature) }

func entry(res *Result, l Level, feature string) *Entry {
	for i := range res.Report.Entries {
		e := &res.Report.Entries[i]
		if e.Level == l && e.Feature == feature {
			return e
		}
	}
	return nil
}

func mustContain(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s: missing %q in:\n%s", what, sub, s)
		}
	}
}

func mustNotContain(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("%s: unexpected %q in:\n%s", what, sub, s)
		}
	}
}

// ---- WSDL 1.1 -------------------------------------------------------------

func TestBankStructureAndHeaders(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	var folders []string
	for _, it := range res.Items {
		if it.Kind == "folder" {
			folders = append(folders, it.Name)
		}
	}
	if got := strings.Join(folders, ","); got != "BankService,BankSoap11Port,BankSoap12Port" {
		t.Fatalf("folders = %s", got)
	}
	if res.Collection.Name != "BankService" || res.Collection.ScopePolicy != store.ScopePolicyBlock {
		t.Fatalf("collection = %+v", res.Collection)
	}
	if res.Report.Stats.Requests != 6 {
		t.Fatalf("requests = %d, want 6 (4 SOAP 1.1 + 2 SOAP 1.2)", res.Report.Stats.Requests)
	}
	reqs := byName(res)
	var ga11, ga12 store.Item
	for _, it := range reqs["GetAccount"] {
		if strings.Contains(urlRaw(t, it), "bank12") {
			ga12 = it
		} else {
			ga11 = it
		}
	}
	if ga11.Method != "POST" || urlRaw(t, ga11) != "https://bank.example.com/ws/bank" {
		t.Fatalf("1.1 request = %s %s", ga11.Method, urlRaw(t, ga11))
	}
	h := headers(t, ga11)
	if h["Content-Type"] != "text/xml; charset=utf-8" || h["SOAPAction"] != `"http://example.com/bank/GetAccount"` {
		t.Fatalf("1.1 headers = %v", h)
	}
	h = headers(t, ga12)
	if h["Content-Type"] != `application/soap+xml; charset=utf-8; action="http://example.com/bank/GetAccount"` || h["SOAPAction"] == "" {
		t.Fatalf("1.2 headers = %v", h)
	}
	mustContain(t, "1.2 envelope", bodyOf(t, ga12), `xmlns:soapenv="http://www.w3.org/2003/05/soap-envelope"`)
	mustContain(t, "1.1 envelope", bodyOf(t, ga11), `xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"`)
	// soapAction="" is declared (empty header); an absent soapAction is omitted
	if h := headers(t, reqs["ListTree"][0]); h["SOAPAction"] != `""` {
		t.Fatalf("ListTree SOAPAction = %q", h["SOAPAction"])
	}
	if h := headers(t, reqs["Ping"][0]); h["SOAPAction"] != "" {
		t.Fatalf("Ping must have no SOAPAction, got %q", h["SOAPAction"])
	}
}

func TestBankEnvelopeSynthesis(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	var tr store.Item
	for _, it := range byName(res)["Transfer"] {
		if !strings.Contains(urlRaw(t, it), "bank12") {
			tr = it
		}
	}
	env := bodyOf(t, tr)
	mustContain(t, "Transfer", env,
		`xmlns:tns="http://example.com/bank"`,
		`xmlns:common="http://example.com/common"`,
		`<tns:TransferRequest channel="?" version="2">`,
		"<tns:from>?</tns:from>",
		"<!--nillable:-->",
		"<tns:to>?</tns:to>",
		"<common:amount>0.0</common:amount>",     // Money.amount, xs:decimal
		"<common:currency>USD</common:currency>", // first enum value
		"<!--Zero or more repetitions:-->",
		"<tns:memo>?</tns:memo>",
		"<!--Optional:-->",
		"<tns:scheduled>2024-01-01T00:00:00Z</tns:scheduled>",
		"CHOICE of the next 3 items",
		"<tns:otp>?</tns:otp>",
		"<tns:priority>NORMAL</tns:priority>", // default value wins
	)
	mustNotContain(t, "Transfer", env, "<tns:token>", "<tns:biometric>")
	if n := strings.Count(env, "<tns:memo>"); n != 1 {
		t.Fatalf("repeated element emitted %d times, want 1", n)
	}
	// choice alternatives are listed in a comment
	mustContain(t, "Transfer", env, "otp | token | biometric")
	if e := entry(res, Degraded, "choice-first-branch"); e == nil || !strings.Contains(e.Message, "token") {
		t.Fatalf("choice not reported: %+v", e)
	}
	// header part synthesized from soap:header
	for _, it := range byName(res)["GetAccount"] {
		if !strings.Contains(urlRaw(t, it), "bank12") {
			mustContain(t, "GetAccount", bodyOf(t, it), "<tns:TraceHeader>", "<tns:traceId>?</tns:traceId>", "<tns:accountId>?</tns:accountId>")
		}
	}
}

func TestAllBodiesWellFormed(t *testing.T) {
	for _, f := range []string{"bank.wsdl", "rpc.wsdl", "service2.wsdl", "soapui.xml"} {
		res := parseFile(t, f)
		for _, it := range res.Items {
			if it.Kind != "request" {
				continue
			}
			if _, err := parseXML([]byte(bodyOf(t, it)), &budget{}); err != nil {
				t.Errorf("%s / %s: body is not well-formed: %v\n%s", f, it.Name, err, bodyOf(t, it))
			}
		}
	}
}

func TestWrappedStyleAndPerOperationReport(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	seen := map[string]string{}
	for _, e := range res.Report.Entries {
		if e.Feature == "operation-style" {
			seen[e.Path] = e.Message
		}
	}
	if got := seen["BankService/BankSoap11Port/GetAccount"]; got != "SOAP 1.1 document/literal (wrapped)" {
		t.Fatalf("style = %q", got)
	}
	if got := seen["BankService/BankSoap12Port/GetAccount"]; got != "SOAP 1.2 document/literal (wrapped)" {
		t.Fatalf("style = %q", got)
	}
	if got := seen["BankService/BankSoap12Port/Transfer"]; got != "SOAP 1.2 document/literal" {
		t.Fatalf("non-wrapped style = %q", got)
	}
	if len(seen) != 6 {
		t.Fatalf("operation-style entries = %d", len(seen))
	}
}

func TestRecursionBounded(t *testing.T) {
	data, _ := os.ReadFile("testdata/bank.wsdl")
	cases := []struct {
		depth, wantLabels int
	}{{0, 3}, {1, 1}, {2, 2}, {3, 3}, {5, 5}, {50, 5}, {-4, 3}}
	for _, c := range cases {
		t.Run("depth"+strconv.Itoa(c.depth), func(t *testing.T) {
			res, err := parseBytes(t, data, Options{MaxDepth: c.depth})
			if err != nil {
				t.Fatal(err)
			}
			var env string
			for _, it := range byName(res)["ListTree"] {
				env = bodyOf(t, it)
			}
			if n := strings.Count(env, "<tns:label>"); n != c.wantLabels {
				t.Fatalf("Node expanded %d times, want %d\n%s", n, c.wantLabels, env)
			}
			mustContain(t, "recursion", env, "recursion limit")
			if !has(res, Degraded, "recursion-limit") {
				t.Fatal("recursion cut not reported")
			}
		})
	}
}

func TestExternalImportReportedNotFetched(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	var locs []string
	for _, e := range res.Report.Entries {
		if e.Level == Degraded && e.Feature == "external-import" {
			locs = append(locs, e.Path)
		}
	}
	if strings.Join(locs, ",") != "http://schemas.example.com/audit.xsd,extra-types.xsd" && strings.Join(locs, ",") != "extra-types.xsd,http://schemas.example.com/audit.xsd" {
		t.Fatalf("external imports = %v", locs)
	}
	// the in-document import (common) must NOT be reported
	for _, l := range locs {
		if strings.Contains(l, "common") {
			t.Fatalf("in-document import wrongly reported: %s", l)
		}
	}
}

// This package's own files must not reach the network or the disk: a WSDL
// names schema locations and a SoapUI project names files, and resolving either
// one would turn an import into an SSRF or a file read. The check is on this
// package's direct imports only -- it deliberately proves nothing about the
// transitive closure, which legitimately pulls in os and net through
// internal/store. Any path under a forbidden root counts, so a future
// net/http/httputil or os/user cannot slip past an exact-match list; net/url is
// allowed because it parses without dialling.
func TestNoNetworkOrDiskImports(t *testing.T) {
	forbiddenRoots := []string{"net", "os", "io/ioutil", "io/fs", "syscall"}
	allowed := map[string]bool{"net/url": true}
	files, _ := filepath.Glob("*.go")
	if len(files) == 0 {
		t.Fatal("no source files found; the glob is relative to the package directory")
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if allowed[p] {
				continue
			}
			for _, root := range forbiddenRoots {
				if p == root || strings.HasPrefix(p, root+"/") {
					t.Errorf("%s imports %s", f, p)
				}
			}
		}
	}
}

func TestPolicyIsReportedNotInvented(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	e := entry(res, NeedsReview, "ws-security-policy")
	if e == nil || !strings.Contains(e.Message, "UsernameToken") {
		t.Fatalf("policy entry = %+v", e)
	}
	for _, it := range byName(res)["Ping"] {
		env := bodyOf(t, it)
		mustContain(t, "Ping", env, "<soapenv:Header>", "add a wsse:Security header")
		mustNotContain(t, "Ping", env, "<wsse:", "Password", "Username")
	}
}

func TestTargetHostReviewEntries(t *testing.T) {
	res := parseFile(t, "bank.wsdl")
	if e := entry(res, NeedsReview, "target-host"); e == nil || e.Path != "bank.example.com" {
		t.Fatalf("target-host = %+v", e)
	}
}

func TestRPCStyles(t *testing.T) {
	res := parseFile(t, "rpc.wsdl")
	reqs := byName(res)
	lk := bodyOf(t, reqs["Lookup"][0])
	mustContain(t, "Lookup", lk,
		`xmlns:tns="urn:inventory"`,
		`soapenv:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"`,
		`<sku xsi:type="xsd:string">?</sku>`,
		`<warehouse xsi:type="xsd:int">0</warehouse>`,
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`,
		`xmlns:xsd="http://www.w3.org/2001/XMLSchema"`)
	add := bodyOf(t, reqs["Add"][0])
	mustContain(t, "Add", add, `<item xsi:type="tns:Item">`, "<sku>?</sku>", "<qty>0</qty>", `<tags xsi:type="tns:StringArray">`, "<item>?</item>")
	note := bodyOf(t, reqs["Note"][0])
	mustContain(t, "Note", note, "<note>?</note>")
	mustNotContain(t, "Note", note, "xsi:type", "encodingStyle")
	styles := map[string]string{}
	for _, e := range res.Report.Entries {
		if e.Feature == "operation-style" {
			styles[e.Path] = e.Message
		}
	}
	if styles["InventoryService/InventoryPort/Lookup"] != "SOAP 1.1 rpc/encoded" || styles["InventoryService/InventoryPort/Note"] != "SOAP 1.1 rpc/literal" {
		t.Fatalf("styles = %v", styles)
	}
	if !has(res, Degraded, "soap-encoded-array") {
		t.Fatal("soap-encoded array not reported")
	}
	if got := urlRaw(t, reqs["Lookup"][0]); got != "http://inventory.example.com:8080/soap" {
		t.Fatalf("url = %s", got)
	}
}

// ---- schema-synthesis table ------------------------------------------------

func wsdlWith(schema, elem string) []byte {
	return []byte(`<?xml version="1.0"?>
<wsdl:definitions targetNamespace="urn:t" xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:tns="urn:t">
<wsdl:types><xs:schema targetNamespace="urn:t" ` + schema + `</xs:schema></wsdl:types>
<wsdl:message name="In"><wsdl:part name="p" element="tns:` + elem + `"/></wsdl:message>
<wsdl:portType name="PT"><wsdl:operation name="` + elem + `"><wsdl:input message="tns:In"/></wsdl:operation></wsdl:portType>
<wsdl:binding name="B" type="tns:PT"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
<wsdl:operation name="` + elem + `"><soap:operation soapAction="a"/><wsdl:input><soap:body use="literal"/></wsdl:input></wsdl:operation></wsdl:binding>
<wsdl:service name="S"><wsdl:port name="P" binding="tns:B"><soap:address location="http://t.example.com/s"/></wsdl:port></wsdl:service>
</wsdl:definitions>`)
}

func TestSchemaSynthesisTable(t *testing.T) {
	cases := []struct {
		name, schema  string
		want, notWant []string
		report        string
	}{
		{"builtin placeholders", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence>
<xs:element name="s" type="xs:string"/><xs:element name="i" type="xs:int"/><xs:element name="l" type="xs:long"/>
<xs:element name="b" type="xs:boolean"/><xs:element name="d" type="xs:double"/><xs:element name="dt" type="xs:dateTime"/>
<xs:element name="da" type="xs:date"/><xs:element name="bin" type="xs:base64Binary"/><xs:element name="pi" type="xs:positiveInteger"/>
</xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:s>?</tns:s>", "<tns:i>0</tns:i>", "<tns:l>0</tns:l>", "<tns:b>false</tns:b>", "<tns:d>0.0</tns:d>", "<tns:dt>2024-01-01T00:00:00Z</tns:dt>", "<tns:da>2024-01-01</tns:da>", "<tns:bin>AA==</tns:bin>", "<tns:pi>1</tns:pi>"}, nil, ""},
		{"maxOccurs zero skipped", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="gone" type="xs:string" maxOccurs="0"/><xs:element name="kept" type="xs:string"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:kept>"}, []string{"gone"}, ""},
		{"required repetition", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="r" type="xs:string" minOccurs="2" maxOccurs="5"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<!--1 or more repetitions:-->", "<tns:r>?</tns:r>"}, nil, ""},
		{"unqualified locals", `elementFormDefault="unqualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="local" type="xs:string"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:E>", "<local>?</local>"}, []string{"tns:local"}, ""},
		{"extension", `elementFormDefault="qualified">
<xs:complexType name="Base"><xs:sequence><xs:element name="b" type="xs:string"/></xs:sequence><xs:attribute name="ba" type="xs:int"/></xs:complexType>
<xs:complexType name="Der"><xs:complexContent><xs:extension base="tns:Base"><xs:sequence><xs:element name="d" type="xs:int"/></xs:sequence><xs:attribute name="da" type="xs:boolean"/></xs:extension></xs:complexContent></xs:complexType>
<xs:element name="E" type="tns:Der"/>`,
			[]string{`ba="0"`, `da="false"`, "<tns:b>?</tns:b>", "<tns:d>0</tns:d>"}, nil, ""},
		{"simpleContent attribute", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:simpleContent><xs:extension base="xs:decimal"><xs:attribute name="cur" type="xs:string" use="required"/></xs:extension></xs:simpleContent></xs:complexType></xs:element>`,
			[]string{`<tns:E cur="?">0.0</tns:E>`}, nil, ""},
		{"attributeGroup and prohibited", `elementFormDefault="qualified">
<xs:attributeGroup name="AG"><xs:attribute name="x" type="xs:int"/><xs:attribute name="nope" type="xs:int" use="prohibited"/></xs:attributeGroup>
<xs:element name="E"><xs:complexType><xs:attributeGroup ref="tns:AG"/></xs:complexType></xs:element>`,
			[]string{`<tns:E x="0"/>`}, []string{"nope"}, ""},
		{"group ref", `elementFormDefault="qualified">
<xs:group name="G"><xs:sequence><xs:element name="g1" type="xs:string"/></xs:sequence></xs:group>
<xs:element name="E"><xs:complexType><xs:group ref="tns:G"/></xs:complexType></xs:element>`,
			[]string{"<tns:g1>?</tns:g1>"}, nil, ""},
		{"list and union and facets", `elementFormDefault="qualified">
<xs:simpleType name="L"><xs:list itemType="xs:int"/></xs:simpleType>
<xs:simpleType name="U"><xs:union memberTypes="xs:boolean xs:string"/></xs:simpleType>
<xs:simpleType name="Min"><xs:restriction base="xs:int"><xs:minInclusive value="7"/></xs:restriction></xs:simpleType>
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="l" type="tns:L"/><xs:element name="u" type="tns:U"/><xs:element name="m" type="tns:Min"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:l>0</tns:l>", "<tns:u>false</tns:u>", "<tns:m>7</tns:m>"}, nil, ""},
		{"nested choice in sequence", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:choice minOccurs="0"><xs:element name="a" type="xs:string"/><xs:sequence><xs:element name="b1" type="xs:string"/></xs:sequence></xs:choice></xs:complexType></xs:element>`,
			[]string{"<!--Optional:-->", "a | (sequence)", "<tns:a>"}, []string{"b1>"}, "choice-first-branch"},
		{"any wildcard", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:any namespace="##other"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"xs:any (namespace=##other)"}, nil, "xs-any"},
		{"unresolved type", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="u" type="ext:Thing" xmlns:ext="urn:elsewhere"/></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:u>?</tns:u>"}, nil, "unresolved-reference"},
		{"special characters escaped", `elementFormDefault="qualified">
<xs:element name="E"><xs:complexType><xs:sequence><xs:element name="e"><xs:simpleType><xs:restriction base="xs:string"><xs:enumeration value="a&lt;b&amp;c"/></xs:restriction></xs:simpleType></xs:element></xs:sequence></xs:complexType></xs:element>`,
			[]string{"<tns:e>a&lt;b&amp;c</tns:e>"}, nil, ""},
		{"abstract type", `elementFormDefault="qualified">
<xs:complexType name="A" abstract="true"><xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence></xs:complexType>
<xs:element name="E" type="tns:A"/>`,
			[]string{"<tns:x>?</tns:x>"}, nil, "abstract-type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := parseBytes(t, wsdlWith(c.schema, "E"), Options{})
			if err != nil {
				t.Fatal(err)
			}
			env := bodyOf(t, byName(res)["E"][0])
			mustContain(t, c.name, env, c.want...)
			mustNotContain(t, c.name, env, c.notWant...)
			if c.report != "" && !has(res, Degraded, c.report) {
				t.Fatalf("missing degraded %q: %+v", c.report, res.Report.Entries)
			}
			if _, err := parseXML([]byte(env), &budget{}); err != nil {
				t.Fatalf("not well-formed: %v", err)
			}
		})
	}
}

// ---- WSDL 2.0 ---------------------------------------------------------------

func TestWSDL2Partial(t *testing.T) {
	res := parseFile(t, "service2.wsdl")
	if res.Report.Format != "wsdl-2.0" || res.Report.Stats.Requests != 1 {
		t.Fatalf("format=%s requests=%d", res.Report.Format, res.Report.Stats.Requests)
	}
	it := byName(res)["greet"][0]
	h := headers(t, it)
	if h["Content-Type"] != `application/soap+xml; charset=utf-8; action="http://example.com/greeting/greet"` {
		t.Fatalf("headers = %v", h)
	}
	mustContain(t, "greet", bodyOf(t, it), "<tns:greet>", "<tns:name>?</tns:name>", "http://www.w3.org/2003/05/soap-envelope")
	for _, f := range []struct {
		l Level
		f string
	}{{Degraded, "wsdl2-partial"}, {Unsupported, "non-soap-binding"}, {Unsupported, "no-input"}} {
		if !has(res, f.l, f.f) {
			t.Errorf("missing %s %s", f.l, f.f)
		}
	}
}

// ---- SoapUI ------------------------------------------------------------------

func TestSoapUIKeepsOwnEnvelopes(t *testing.T) {
	res := parseFile(t, "soapui.xml")
	if res.Collection.Name != "Bank Project" {
		t.Fatalf("name = %q", res.Collection.Name)
	}
	reqs := byName(res)
	ga := reqs["GetAccount - Request 1"]
	if len(ga) != 1 {
		t.Fatalf("GetAccount requests = %d (%v)", len(ga), reqs)
	}
	body := bodyOf(t, ga[0])
	mustContain(t, "stored", body, "${#Project#acct}", "<ban:accountId>")
	if bytes.Contains([]byte(body), []byte("tns:")) {
		t.Fatal("stored envelope must not be re-synthesized")
	}
	h := headers(t, ga[0])
	if h["SOAPAction"] != `"http://example.com/bank/GetAccount"` || h["X-Api-Key"] != "abc123" {
		t.Fatalf("headers = %v", h)
	}
	// Transfer had no stored request: synthesized from the cached WSDL
	tr := reqs["Transfer"]
	if len(tr) != 1 {
		t.Fatalf("Transfer = %d", len(tr))
	}
	mustContain(t, "synth", bodyOf(t, tr[0]), "<tns:TransferRequest channel=\"?\"", "CHOICE")
	// Ping uses an empty-string action
	if h := headers(t, reqs["Ping - Request 1"][0]); h["SOAPAction"] != `""` {
		t.Fatalf("Ping action = %q", h["SOAPAction"])
	}
	// test step request imported
	if len(reqs["Ping step"]) != 1 {
		t.Fatalf("test-step request missing: %v", reqs)
	}
	for _, f := range []struct {
		l Level
		f string
	}{
		{Unsupported, "soapui-rest-service"}, {Unsupported, "soapui-mock-service"}, {Unsupported, "soapui-test-step"},
		{Unsupported, "soapui-properties"}, {Blocked, "groovy-script-not-imported"},
		{Degraded, "soapui-property-expansion"}, {NeedsReview, "soapui-credentials"}, {NeedsReview, "embedded-credential"},
	} {
		if !has(res, f.l, f.f) {
			t.Errorf("missing %s %s", f.l, f.f)
		}
	}
	// credentials and header secrets are flagged but never echoed in the report
	raw, _ := json.Marshal(res.Report)
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("secret leaked into the report")
	}
	if !has(res, NeedsReview, "embedded-credential") {
		t.Fatal("literal credential header not flagged")
	}
}

func TestSoapUIEmbeddedDTDRefusedNotFatal(t *testing.T) {
	proj := `<?xml version="1.0"?><con:soapui-project name="p" xmlns:con="http://eviware.com/soapui/config" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<con:interface xsi:type="con:WsdlInterface" name="I" bindingName="{urn:x}B" soapVersion="1_1">
<con:definitionCache><con:part><con:url>file:/x.wsdl</con:url><con:content>&lt;!DOCTYPE d [&lt;!ENTITY x SYSTEM "file:///etc/passwd"&gt;]&gt;&lt;definitions xmlns="http://schemas.xmlsoap.org/wsdl/"/&gt;</con:content></con:part></con:definitionCache>
<con:operation name="Op" action="a"><con:call name="R"><con:endpoint>http://t.example.com/</con:endpoint><con:request>&lt;e/&gt;</con:request></con:call></con:operation>
</con:interface></con:soapui-project>`
	res, err := parseBytes(t, []byte(proj), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !has(res, Blocked, "soapui-definition-refused") || res.Report.Stats.Requests != 1 {
		t.Fatalf("report = %+v", res.Report.Entries)
	}
}

// ---- hostile / malformed input -----------------------------------------------

func TestMalformedXMLFailsLoudly(t *testing.T) {
	cases := map[string]string{
		"empty":            ``,
		"not xml":          `{"openapi":"3.0.0"}`,
		"truncated":        `<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"><message name="a">`,
		"mismatched tags":  `<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"><message></types></definitions>`,
		"two roots":        `<a/><b/>`,
		"text outside":     `<a/>junk`,
		"bad entity":       `<definitions xmlns="http://schemas.xmlsoap.org/wsdl/" name="&nope;"/>`,
		"unterminated tag": `<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := Parse([]byte(in), Options{})
			if !errors.Is(err, ErrMalformed) || res != nil {
				t.Fatalf("err = %v, res = %v; want ErrMalformed", err, res)
			}
		})
	}
}

func TestWrongFormat(t *testing.T) {
	for name, in := range map[string]string{
		"html":       `<html/>`,
		"bare xsd":   `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`,
		"wrong ns":   `<definitions xmlns="urn:other"/>`,
		"soap env":   `<Envelope xmlns="http://schemas.xmlsoap.org/soap/envelope/"/>`,
		"no ns wsdl": `<definitions/>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in), Options{}); !errors.Is(err, ErrNotSOAP) {
				t.Fatalf("err = %v, want ErrNotSOAP", err)
			}
		})
	}
}

func TestXXERefused(t *testing.T) {
	for _, f := range []string{"xxe.wsdl", "laughs.wsdl"} {
		t.Run(f, func(t *testing.T) {
			data, _ := os.ReadFile("testdata/" + f)
			start := time.Now()
			res, err := Parse(data, Options{})
			if !errors.Is(err, ErrDTD) || res != nil {
				t.Fatalf("err = %v, res = %v; want ErrDTD", err, res)
			}
			if time.Since(start) > time.Second {
				t.Fatal("refusal must be immediate")
			}
			if strings.Contains(err.Error(), "root:") {
				t.Fatal("error leaked file content")
			}
		})
	}
	// entity references without a DOCTYPE cannot resolve either
	_, err := Parse([]byte(`<definitions xmlns="http://schemas.xmlsoap.org/wsdl/" name="&xxe;"/>`), Options{})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("undeclared entity: %v", err)
	}
	// parameter entities / external subsets
	for _, in := range []string{
		`<!DOCTYPE d SYSTEM "http://attacker.example.com/evil.dtd"><definitions xmlns="http://schemas.xmlsoap.org/wsdl/"/>`,
		`<!DOCTYPE d [<!ENTITY % p SYSTEM "file:///etc/hosts"> %p;]><definitions xmlns="http://schemas.xmlsoap.org/wsdl/"/>`,
		`<?xml version="1.0"?><!ENTITY x "y"><definitions xmlns="http://schemas.xmlsoap.org/wsdl/"/>`,
	} {
		if _, err := Parse([]byte(in), Options{}); !errors.Is(err, ErrDTD) {
			t.Errorf("%q: err = %v, want ErrDTD", in[:30], err)
		}
	}
	// XXE inside an XSD embedded in a WSDL is refused as well
	_, err = Parse([]byte(`<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"><!DOCTYPE x></wsdl:definitions>`), Options{})
	if !errors.Is(err, ErrDTD) {
		t.Fatalf("nested doctype: %v", err)
	}
}

func TestBoundsInputSize(t *testing.T) {
	if _, err := Parse(make([]byte, MaxInputBytes+1), Options{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestBoundsXMLNestingAndNodeCount(t *testing.T) {
	deep := strings.Repeat("<a>", maxXMLDepth+5) + strings.Repeat("</a>", maxXMLDepth+5)
	if _, err := Parse([]byte(deep), Options{}); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("deep: %v", err)
	}
	wide := `<definitions xmlns="http://schemas.xmlsoap.org/wsdl/">` + strings.Repeat("<a/>", maxXMLNodes+10) + `</definitions>`
	if _, err := Parse([]byte(wide), Options{}); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("wide: %v", err)
	}
}

func TestBoundsOperationCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<wsdl:definitions targetNamespace="urn:t" xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:tns="urn:t">
<wsdl:types><xs:schema targetNamespace="urn:t"><xs:element name="E" type="xs:string"/></xs:schema></wsdl:types>
<wsdl:message name="In"><wsdl:part name="p" element="tns:E"/></wsdl:message><wsdl:portType name="PT">`)
	n := MaxOperations + 20
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<wsdl:operation name="op%d"><wsdl:input message="tns:In"/></wsdl:operation>`, i)
	}
	b.WriteString(`</wsdl:portType><wsdl:binding name="B" type="tns:PT"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<wsdl:operation name="op%d"><soap:operation soapAction="a"/><wsdl:input><soap:body use="literal"/></wsdl:input></wsdl:operation>`, i)
	}
	b.WriteString(`</wsdl:binding><wsdl:service name="S"><wsdl:port name="P" binding="tns:B"><soap:address location="http://t.example.com/s"/></wsdl:port></wsdl:service></wsdl:definitions>`)
	res, err := parseBytes(t, []byte(b.String()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Stats.Requests != MaxOperations || !has(res, Blocked, "too-many-operations") {
		t.Fatalf("requests=%d blocked=%v", res.Report.Stats.Requests, has(res, Blocked, "too-many-operations"))
	}
}

// A schema whose distinct types fan out exponentially must be cut by the
// envelope element cap, not run away.
func TestBoundsEnvelopeFanOut(t *testing.T) {
	var s strings.Builder
	s.WriteString(`elementFormDefault="qualified">`)
	const levels, fan = 12, 8
	for l := 0; l < levels; l++ {
		fmt.Fprintf(&s, `<xs:complexType name="T%d"><xs:sequence>`, l)
		for f := 0; f < fan; f++ {
			if l == levels-1 {
				fmt.Fprintf(&s, `<xs:element name="f%d" type="xs:string"/>`, f)
			} else {
				fmt.Fprintf(&s, `<xs:element name="f%d" type="tns:T%d"/>`, f, l+1)
			}
		}
		s.WriteString(`</xs:sequence></xs:complexType>`)
	}
	s.WriteString(`<xs:element name="E" type="tns:T0"/>`)
	start := time.Now()
	res, err := parseBytes(t, wsdlWith(s.String(), "E"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("fan-out took %s", time.Since(start))
	}
	if !has(res, Degraded, "envelope-truncated") {
		t.Fatal("truncation not reported")
	}
	env := bodyOf(t, byName(res)["E"][0])
	if len(env) > maxEnvelopeSize+4096 {
		t.Fatalf("envelope is %d bytes", len(env))
	}
	if n := strings.Count(env, "\n"); n > maxEnvElements*2+10 {
		t.Fatalf("envelope has %d lines", n)
	}
}

// ---- misc ---------------------------------------------------------------------

func TestDeterministicAndBundle(t *testing.T) {
	a, _ := json.Marshal(parseFile(t, "bank.wsdl"))
	b, _ := json.Marshal(parseFile(t, "bank.wsdl"))
	if !bytes.Equal(a, b) {
		t.Fatal("two parses of the same input differ")
	}
	res := parseFile(t, "bank.wsdl")
	bd := res.Bundle()
	if len(bd.Collections) != 1 || len(bd.Items) != len(res.Items) {
		t.Fatalf("bundle = %+v", bd)
	}
	var rep Report
	if err := json.Unmarshal(res.Collection.ImportReport, &rep); err != nil || rep.Format != "wsdl-1.1" || rep.Headline == "" {
		t.Fatalf("import report = %v / %+v", err, rep)
	}
}

func TestNoOperationsIsAnError(t *testing.T) {
	res, err := Parse([]byte(`<definitions xmlns="http://schemas.xmlsoap.org/wsdl/" targetNamespace="urn:x"/>`), Options{})
	if !errors.Is(err, ErrEmpty) || res == nil {
		t.Fatalf("err = %v res = %v", err, res)
	}
}

func TestNonHTTPEndpointAndTransportSkipped(t *testing.T) {
	data := bytes.ReplaceAll(wsdlWith(`><xs:element name="E" type="xs:string"/>`, "E"), []byte("http://t.example.com/s"), []byte("jms:queue:Q"))
	res, err := parseBytes(t, data, Options{})
	if !errors.Is(err, ErrEmpty) || !has(res, Unsupported, "non-http-endpoint") {
		t.Fatalf("err=%v entries=%+v", err, res.Report.Entries)
	}
}
