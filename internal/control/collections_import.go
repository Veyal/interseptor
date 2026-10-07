package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/curl"
	"github.com/Veyal/interseptor/internal/collimport/openapi"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// parseCollectionImport decodes an uploaded file without storing, executing or
// fetching anything. Postman, OpenAPI/Swagger, curl, Insomnia, Bruno and
// HAR/Burp are supported; every format is normalised to a postman.Result so
// preview and commit share one path. "auto" sniffs the content.
func parseCollectionImport(data []byte, format string) (*postman.Result, int, error) {
	switch f := strings.ToLower(format); f {
	case "", "auto":
		return parseAutoImport(data)
	case "postman":
		return parsePostmanImport(data)
	case "openapi", "swagger":
		return parseOpenAPIImport(data)
	case "curl":
		return parseCurlImport(data)
	case "insomnia":
		return parseInsomniaImport(data)
	case "bruno":
		return parseBrunoImport(data)
	case "bruno-files":
		return parseBrunoFilesImport(data)
	case "har":
		return parseHARImport(data)
	case "burp":
		return parseBurpImport(data)
	default:
		return nil, http.StatusBadRequest, errors.New("import format " + format + " is not supported (supported: " + importFormatsList + ")")
	}
}

func parseAutoImport(data []byte) (*postman.Result, int, error) {
	switch {
	case looksLikeCurl(data):
		return parseCurlImport(data)
	case looksLikeBru(data):
		return parseBrunoImport(data)
	case looksLikeXML(data):
		return parseBurpImport(data)
	case json.Valid(data):
		return sniffJSON(data)
	case looksLikeInsomniaYAML(data):
		return parseInsomniaImport(data)
	}
	res, code, err := parseOpenAPIImport(data)
	if err != nil && code == http.StatusUnsupportedMediaType {
		return nil, http.StatusBadRequest, errors.New("the file is not valid JSON, YAML, a curl command, a .bru file or Burp XML")
	}
	return res, code, err
}

func looksLikeCurl(data []byte) bool {
	t := strings.TrimLeft(string(data), " \t\r\n")
	return strings.HasPrefix(t, "curl ") || strings.HasPrefix(t, "curl\t")
}

func parsePostmanImport(data []byte) (*postman.Result, int, error) {
	if !json.Valid(data) {
		return nil, http.StatusBadRequest, errors.New("the file is not valid JSON")
	}
	res, err := postman.Parse(data, postman.Options{})
	switch {
	case err == nil:
		return res, 0, nil
	case errors.Is(err, postman.ErrTooLarge):
		return nil, http.StatusRequestEntityTooLarge, err
	case errors.Is(err, postman.ErrUnsupported), errors.Is(err, postman.ErrNotPostman):
		return nil, http.StatusUnsupportedMediaType, err
	default:
		return nil, http.StatusBadRequest, errors.New("could not parse the file: " + err.Error())
	}
}

func parseOpenAPIImport(data []byte) (*postman.Result, int, error) {
	r, err := openapi.Parse(data, openapi.Options{})
	if err != nil {
		switch {
		case errors.Is(err, openapi.ErrTooLarge):
			return nil, http.StatusRequestEntityTooLarge, err
		case errors.Is(err, openapi.ErrNotOpenAPI), errors.Is(err, openapi.ErrUnsupported):
			return nil, http.StatusUnsupportedMediaType, err
		}
		return nil, http.StatusBadRequest, errors.New("could not parse the file: " + err.Error())
	}
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items,
		Variables: r.Variables, Environments: r.Environments, Report: r.Report}, 0, nil
}

func parseCurlImport(data []byte) (*postman.Result, int, error) {
	r, err := curl.Parse(data, curl.Options{})
	if err != nil {
		switch {
		case errors.Is(err, curl.ErrTooLarge):
			return nil, http.StatusRequestEntityTooLarge, err
		case errors.Is(err, curl.ErrNoCurl):
			return nil, http.StatusUnsupportedMediaType, err
		}
		return nil, http.StatusBadRequest, errors.New("could not parse the curl command: " + err.Error())
	}
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items,
		Report: r.Report}, 0, nil
}

type importPreview struct {
	Kind         postman.Kind   `json:"kind"`
	Name         string         `json:"name,omitempty"`
	Report       postman.Report `json:"report"`
	Environments []string       `json:"environments,omitempty"`
	Quarantined  bool           `json:"scriptsQuarantined"`
}

func (c *collectionsAPI) readImportBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return readLimitedBody(w, r, maxCollectionImportBytes)
}

// importPreview parses and reports. Nothing is written.
func (c *collectionsAPI) importPreview(w http.ResponseWriter, r *http.Request) {
	data, ok := c.readImportBody(w, r)
	if !ok {
		return
	}
	res, code, err := parseCollectionImport(data, r.URL.Query().Get("format"))
	if err != nil {
		httpErr(w, code, err.Error())
		return
	}
	pv := importPreview{Kind: res.Kind, Report: res.Report, Quarantined: true}
	if res.Kind == postman.KindCollection {
		pv.Name = res.Collection.Name
	}
	for _, e := range res.Environments {
		pv.Environments = append(pv.Environments, e.Environment.Name)
	}
	writeJSON(w, http.StatusOK, pv)
}

// importCommit stores a parsed import. Scripts arrive quarantined: the store
// merge never carries trust and grants no capabilities.
func (c *collectionsAPI) importCommit(w http.ResponseWriter, r *http.Request) {
	data, ok := c.readImportBody(w, r)
	if !ok {
		return
	}
	res, code, err := parseCollectionImport(data, r.URL.Query().Get("format"))
	if err != nil {
		httpErr(w, code, err.Error())
		return
	}
	c.mu.Lock()
	stats, err := c.h.st.ImportCollectionsBundle(res.Bundle())
	c.mu.Unlock()
	if err != nil {
		collErr(w, err)
		return
	}
	var logged int
	storedUID := ""
	if res.Kind == postman.KindCollection {
		storedUID = c.storedCollectionUID(res.Collection)
		if raw, err := json.Marshal(res.Report); err == nil {
			if err := c.h.st.AddImportLog(storedUID, importSource(res.Report.Format), string(raw)); err == nil {
				logged = 1
			}
		}
	}
	// Secret-typed values the file carried become local current values only.
	for _, sv := range res.SecretValues {
		if _, err := c.setCurrentValue(sv.OwnerKind, sv.OwnerUID, sv.Key, sv.Value, "import", true); err != nil && !errors.Is(err, store.ErrCollNotFound) {
			continue
		}
	}
	out := map[string]any{"stats": stats, "report": res.Report, "scriptsQuarantined": true, "importLogged": logged == 1}
	if res.Kind == postman.KindCollection {
		out["collectionUid"] = storedUID
	}
	writeJSON(w, http.StatusCreated, out)
}

// storedCollectionUID names the collection an import landed in. A file that
// matches an existing collection (same uid or name) is merged into it, so the
// parsed uid may never have been written; answering with it would hand the
// caller a collection that 404s.
func (c *collectionsAPI) storedCollectionUID(parsed store.Collection) string {
	if _, err := c.h.st.GetCollection(parsed.UID); err == nil {
		return parsed.UID
	}
	all, err := c.h.st.ListCollections()
	if err != nil {
		return parsed.UID
	}
	for _, co := range all {
		if strings.EqualFold(co.Name, parsed.Name) {
			return co.UID
		}
	}
	return parsed.UID
}

// importSource names the importer for the import log; Postman reports leave
// Format empty.
func importSource(format string) string {
	if format == "" {
		return "postman"
	}
	return format
}
