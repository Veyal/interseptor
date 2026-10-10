package control

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/bruno"
	"github.com/Veyal/interseptor/internal/collimport/graphql"
	"github.com/Veyal/interseptor/internal/collimport/har"
	"github.com/Veyal/interseptor/internal/collimport/httpfile"
	"github.com/Veyal/interseptor/internal/collimport/insomnia"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/collimport/soap"
)

// The Insomnia, Bruno and HAR/Burp importers join Postman, OpenAPI and curl
// behind the same preview/commit routes. Every format is normalised to a
// postman.Result so preview and commit share one path, and every script
// arrives quarantined (the store merge carries no trust and no capabilities).

const importFormatsList = "auto, postman, openapi, curl, insomnia, bruno, bruno-files, har, burp, graphql, httpfile, soap"

func parseInsomniaImport(data []byte) (*postman.Result, int, error) {
	r, err := insomnia.Parse(data, insomnia.Options{})
	if err != nil {
		switch {
		case errors.Is(err, insomnia.ErrTooLarge):
			return nil, http.StatusRequestEntityTooLarge, err
		case errors.Is(err, insomnia.ErrNotInsomnia), errors.Is(err, insomnia.ErrNothingFound):
			return nil, http.StatusUnsupportedMediaType, err
		}
		return nil, http.StatusBadRequest, errors.New("could not parse the Insomnia export: " + err.Error())
	}
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Variables: r.Variables,
		Environments: r.Environments, Report: r.Report, SecretValues: r.SecretValues}, 0, nil
}

func brunoResult(r *bruno.Result) *postman.Result {
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Variables: r.Variables,
		Environments: r.Environments, Report: r.Report, SecretValues: r.SecretValues}
}

func brunoErr(err error) (int, error) {
	switch {
	case errors.Is(err, bruno.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err
	case errors.Is(err, bruno.ErrNoBru), errors.Is(err, bruno.ErrNotBru):
		return http.StatusUnsupportedMediaType, err
	}
	return http.StatusBadRequest, errors.New("could not parse the Bruno file: " + err.Error())
}

func parseBrunoImport(data []byte) (*postman.Result, int, error) {
	r, err := bruno.Parse(data, bruno.Options{})
	if err != nil {
		code, e := brunoErr(err)
		return nil, code, e
	}
	return brunoResult(r), 0, nil
}

// parseBrunoFilesImport reads a Bruno collection folder sent as
// {files:[{path,text}]}: paths are logical (the package never touches disk).
func parseBrunoFilesImport(data []byte) (*postman.Result, int, error) {
	var in struct {
		Files []struct {
			Path string `json:"path"`
			Text string `json:"text"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &in); err != nil || len(in.Files) == 0 {
		return nil, http.StatusBadRequest, errors.New(`bruno-files expects {"files":[{"path":"...","text":"..."}]}`)
	}
	files := make([]bruno.File, 0, len(in.Files))
	for _, f := range in.Files {
		files = append(files, bruno.File{Path: f.Path, Data: []byte(f.Text)})
	}
	r, err := bruno.ParseFiles(files, bruno.Options{})
	if err != nil {
		code, e := brunoErr(err)
		return nil, code, e
	}
	return brunoResult(r), 0, nil
}

func harResult(r *har.Result) *postman.Result {
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Report: r.Report}
}

func harErr(err error, what string) (int, error) {
	switch {
	case errors.Is(err, har.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err
	case errors.Is(err, har.ErrEmpty):
		return http.StatusUnsupportedMediaType, err
	}
	return http.StatusBadRequest, errors.New("could not parse the " + what + " file: " + err.Error())
}

func parseHARImport(data []byte) (*postman.Result, int, error) {
	r, err := har.ParseHAR(data, har.Options{})
	if err != nil {
		code, e := harErr(err, "HAR")
		return nil, code, e
	}
	return harResult(r), 0, nil
}

func parseBurpImport(data []byte) (*postman.Result, int, error) {
	r, err := har.ParseBurp(bytes.NewReader(data), har.Options{})
	if err != nil {
		code, e := harErr(err, "Burp")
		return nil, code, e
	}
	return harResult(r), 0, nil
}

func looksLikeBru(data []byte) bool {
	t := strings.TrimLeft(string(data), " \t\r\n\ufeff")
	return strings.HasPrefix(t, "meta {") || strings.HasPrefix(t, "meta{")
}

func looksLikeXML(data []byte) bool {
	t := strings.TrimLeft(string(data), " \t\r\n\ufeff")
	return strings.HasPrefix(t, "<?xml") || strings.HasPrefix(t, "<items")
}

func looksLikeInsomniaYAML(data []byte) bool {
	head := string(data)
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(head, "collection.insomnia.rest")
}

// sniffJSON picks the importer for a JSON document by its top-level keys; a
// document with no telltale key falls back to trying each importer in turn.
func sniffJSON(data []byte) (*postman.Result, int, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) == nil {
		switch {
		case top["log"] != nil:
			return parseHARImport(data)
		case top["_type"] != nil || top["resources"] != nil || top["__export_format"] != nil:
			return parseInsomniaImport(data)
		case top["openapi"] != nil || top["swagger"] != nil:
			return parseOpenAPIImport(data)
		}
	}
	return sniffByTrial(data, top)
}

// sniffByTrial tries each JSON importer in turn for a document with no
// telltale key. It surfaces the error of the importer that matched most
// strongly: one that recognised the format but could not parse it, else the
// Postman error for a Postman-looking file, else a list of what was tried.
func sniffByTrial(data []byte, top map[string]json.RawMessage) (*postman.Result, int, error) {
	type importer struct {
		name  string
		parse func([]byte) (*postman.Result, int, error)
	}
	var tried []string
	var postmanRes *postman.Result
	var postmanCode int
	var postmanErr error
	for i, imp := range []importer{{"Postman", parsePostmanImport}, {"OpenAPI", parseOpenAPIImport},
		{"Insomnia", parseInsomniaImport}, {"HAR", parseHARImport}} {
		res, code, err := imp.parse(data)
		if err == nil {
			return res, 0, nil
		}
		if i == 0 {
			postmanRes, postmanCode, postmanErr = res, code, err
		}
		// A recognised-but-broken file surfaces its own importer's error. HAR is
		// excluded: it reports "missing log" as a parse error for any JSON, and a
		// real HAR (top-level "log") never reaches this fallback.
		if (code != http.StatusUnsupportedMediaType && imp.name != "HAR") || (i == 0 && errors.Is(err, postman.ErrUnsupported)) {
			return res, code, err
		}
		tried = append(tried, imp.name+": "+err.Error())
	}
	for _, k := range []string{"info", "item", "values", "collection"} {
		if _, ok := top[k]; ok {
			return postmanRes, postmanCode, postmanErr
		}
	}
	return nil, http.StatusUnsupportedMediaType, errors.New("the file is JSON but not a recognised collection format; tried " + strings.Join(tried, "; "))
}

// --- GraphQL, .http/.rest and SOAP/WSDL ---------------------------------
//
// These three join the same preview/commit path as the rest: each returns a
// postman.Result so one commit path serves every format. Detection order
// matters. GraphQL and SOAP have strong markers (a __schema/queryType pair, or
// a WSDL/SoapUI root element) and can be sniffed confidently. The .http
// heuristic is the weakest of all the importers, so it runs last, after every
// structured format has declined.

func parseGraphQLImport(data []byte) (*postman.Result, int, error) {
	res, err := graphql.Parse(data, graphql.Options{})
	switch {
	case errors.Is(err, graphql.ErrTooLarge):
		return nil, http.StatusRequestEntityTooLarge, errors.New("the GraphQL schema is too large")
	case errors.Is(err, graphql.ErrNotGraphQL):
		return nil, http.StatusUnsupportedMediaType, errors.New("graphql: not a GraphQL schema or introspection result")
	case err != nil:
		return nil, http.StatusBadRequest, errors.New("graphql: " + err.Error())
	}
	return res, 0, nil
}

func parseHTTPFileImport(data []byte) (*postman.Result, int, error) {
	res, err := httpfile.Parse(data, httpfile.Options{})
	switch {
	case errors.Is(err, httpfile.ErrTooLarge):
		return nil, http.StatusRequestEntityTooLarge, errors.New("the .http file is too large")
	case errors.Is(err, httpfile.ErrNotHTTPFile):
		return nil, http.StatusBadRequest, errors.New("httpfile: no requests found in the file")
	case err != nil:
		return nil, http.StatusBadRequest, errors.New("httpfile: " + err.Error())
	}
	return res, 0, nil
}

func parseSOAPImport(data []byte) (*postman.Result, int, error) {
	res, err := soap.Parse(data, soap.Options{})
	switch {
	case errors.Is(err, soap.ErrTooLarge):
		return nil, http.StatusRequestEntityTooLarge, errors.New("the WSDL or SoapUI project is too large")
	case errors.Is(err, soap.ErrNotSOAP):
		return nil, http.StatusUnsupportedMediaType, errors.New("soap: not a WSDL or SoapUI project")
	case errors.Is(err, soap.ErrDTD):
		// A DTD in an engagement artefact is a hostile-input signal, not a parse nicety.
		return nil, http.StatusBadRequest, errors.New("soap: the document declares a DTD or entity, which is refused")
	case err != nil && res == nil:
		// The package already prefixes its errors; adding another gives "soap: soap: ...".
		return nil, http.StatusBadRequest, err
	}
	return soapResult(res), 0, nil
}

func soapResult(r *soap.Result) *postman.Result {
	if r == nil {
		return nil
	}
	return &postman.Result{
		Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items,
		Variables: r.Variables, Environments: r.Environments, Report: r.Report,
	}
}

// looksLikeSOAP reports whether the document's ROOT element is a WSDL 1.1/2.0
// or SoapUI root, so SOAP XML is not mistaken for Burp's saved-items XML (both
// start with <?xml). It must test the root specifically: a substring match
// would claim any document that merely contains a <description> element,
// including ordinary Burp exports.
func looksLikeSOAP(data []byte) bool {
	name := xmlRootLocalName(data)
	return name == "definitions" || name == "description" || name == "soapui-project"
}

// xmlRootLocalName returns the local name of the first element, skipping the
// XML declaration, comments, DOCTYPE and processing instructions. It returns ""
// when no element is found in the inspected prefix.
func xmlRootLocalName(data []byte) string {
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	dec := xml.NewDecoder(bytes.NewReader(head))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}
