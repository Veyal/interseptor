package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// parseCollectionImport decodes an uploaded file without storing, executing or
// fetching anything. Only Postman is wired in this build; other formats are
// refused with a clear message instead of being guessed at.
func parseCollectionImport(data []byte, format string) (*postman.Result, int, error) {
	switch strings.ToLower(format) {
	case "", "auto", "postman":
	default:
		return nil, http.StatusBadRequest, errors.New("import format " + format + " is not supported yet (supported: auto, postman)")
	}
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
	if res.Kind == postman.KindCollection {
		if raw, err := json.Marshal(res.Report); err == nil {
			if err := c.h.st.AddImportLog(res.Collection.UID, "postman", string(raw)); err == nil {
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
		out["collectionUid"] = res.Collection.UID
	}
	writeJSON(w, http.StatusCreated, out)
}
