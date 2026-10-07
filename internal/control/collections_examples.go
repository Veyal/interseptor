package control

import (
	"encoding/json"
	"net/http"

	"github.com/Veyal/interseptor/internal/store"
)

// Examples live in the item's examples column as a JSON array of objects, each
// with a string "id". These routes give them CRUD without the UI having to
// rewrite the whole item.

func parseExamples(raw json.RawMessage) ([]map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func exampleID(e map[string]json.RawMessage) string {
	var id string
	_ = json.Unmarshal(e["id"], &id)
	return id
}

func (c *collectionsAPI) saveExamples(r *http.Request, it *store.Item, ex []map[string]json.RawMessage) (*store.Item, error) {
	raw, err := json.Marshal(ex)
	if err != nil {
		return nil, err
	}
	next := *it
	next.Examples, next.Rev = raw, 0
	return c.h.st.UpdateItem(next, actorFor(r))
}

func (c *collectionsAPI) listExamples(w http.ResponseWriter, r *http.Request) {
	it, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	ex, err := parseExamples(it.Examples)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "stored examples are malformed")
		return
	}
	if ex == nil {
		ex = []map[string]json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"examples": ex})
}

func (c *collectionsAPI) createExample(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &in) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	ex, err := parseExamples(it.Examples)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "stored examples are malformed")
		return
	}
	if len(ex) >= maxExamplesPerReq {
		httpErr(w, http.StatusBadRequest, "too many examples on this request")
		return
	}
	id, _ := json.Marshal(store.NewUID())
	in["id"] = id
	if _, err := c.saveExamples(r, it, append(ex, in)); err != nil {
		collErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (c *collectionsAPI) updateExample(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &in) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	ex, err := parseExamples(it.Examples)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "stored examples are malformed")
		return
	}
	id := r.PathValue("id")
	for i := range ex {
		if exampleID(ex[i]) == id {
			rawID, _ := json.Marshal(id)
			in["id"] = rawID
			ex[i] = in
			if _, err := c.saveExamples(r, it, ex); err != nil {
				collErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, in)
			return
		}
	}
	httpErr(w, http.StatusNotFound, "example not found")
}

func (c *collectionsAPI) deleteExample(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, err := c.h.st.GetItem(r.PathValue("uid"))
	if err != nil {
		collErr(w, err)
		return
	}
	ex, err := parseExamples(it.Examples)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "stored examples are malformed")
		return
	}
	id := r.PathValue("id")
	for i := range ex {
		if exampleID(ex[i]) == id {
			if _, err := c.saveExamples(r, it, append(ex[:i:i], ex[i+1:]...)); err != nil {
				collErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	httpErr(w, http.StatusNotFound, "example not found")
}
