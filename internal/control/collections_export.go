package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	curlexp "github.com/Veyal/interseptor/internal/collexport/curl"
	nativeexp "github.com/Veyal/interseptor/internal/collexport/native"
	pmexp "github.com/Veyal/interseptor/internal/collexport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// exportFormats is the supported list, in the order the error message shows.
var exportFormats = []string{"postman", "curl", "native"}

// export serves GET /api/collections/{uid}/export?format=postman|curl|native.
//
// Secrets: the bundle always comes from scrubbedBundle (the single scrub) and
// no exporter is ever given IncludeSecrets. There is deliberately no
// unscrubbed form for any caller, UI session included: a parameter that asks
// for one is refused rather than ignored, so a client never believes it got
// credentials it did not.
func (c *collectionsAPI) export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for _, k := range []string{"secrets", "includeSecrets", "reveal", "scrub", "raw"} {
		if _, ok := q[k]; ok {
			httpErr(w, http.StatusBadRequest, "collection exports are always secret-scrubbed; there is no option to include secrets")
			return
		}
	}
	format := strings.ToLower(strings.TrimSpace(q.Get("format")))
	known := false
	for _, f := range exportFormats {
		known = known || f == format
	}
	if !known {
		httpErr(w, http.StatusBadRequest, "unsupported export format "+quoteShort(format)+"; supported: "+strings.Join(exportFormats, ", "))
		return
	}
	uid := r.PathValue("uid")
	b, err := c.scrubbedBundle()
	if err != nil {
		collErr(w, err)
		return
	}
	hardenExportVariables(&b)
	var name string
	for _, col := range b.Collections {
		if col.UID == uid {
			name = col.Name
		}
	}
	if name == "" && !bundleHasCollection(b, uid) {
		httpErr(w, http.StatusNotFound, "not found")
		return
	}
	var (
		data         []byte
		ctype, extra string
	)
	switch format {
	case "postman":
		var out *pmexp.Output
		out, err = pmexp.ExportCollection(b, uid, pmexp.Options{})
		if out != nil {
			data = out.Data
		}
		ctype, extra = "application/json; charset=utf-8", ".postman_collection.json"
	case "curl":
		var out *curlexp.Output
		out, err = curlexp.ExportCollection(b, uid, curlexp.Options{Shebang: true})
		if out != nil {
			data = out.Data
		}
		ctype, extra = "text/x-shellscript; charset=utf-8", ".sh"
	default:
		var out *nativeexp.Output
		out, err = nativeexp.Export(b, uid, nativeexp.Options{})
		if out != nil {
			data = out.Data
		}
		ctype, extra = "application/json; charset=utf-8", ".ixcol.json"
	}
	if err != nil {
		if errors.Is(err, pmexp.ErrNoCollection) || errors.Is(err, curlexp.ErrNoCollection) || errors.Is(err, nativeexp.ErrNoCollection) {
			httpErr(w, http.StatusNotFound, "not found")
			return
		}
		httpInternalErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+exportFileStem(name)+extra+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// hardenExportVariables is defence in depth on top of the store scrub: a
// variable whose name looks like a credential (token, api_key, ...) is treated
// as secret-typed for the export even when it was imported as a plain default
// variable, so its initial value cannot leave in a file. The slice is copied
// first; the store's data is never modified.
func hardenExportVariables(b *store.CollectionsBundle) {
	vars := make([]store.Variable, len(b.Variables))
	copy(vars, b.Variables)
	for i := range vars {
		if vars[i].Type != store.VarTypeSecret && store.IsSecretName(vars[i].Key) {
			vars[i].Type = store.VarTypeSecret
			vars[i].InitialValue = ""
		}
	}
	b.Variables = vars

	// Sidecars keep the original foreign document (Postman variable[] and
	// auth arrays) byte-exact so a re-export is lossless. The store scrub does
	// not walk them, so blank literal credentials here before any exporter
	// sees or carries them.
	cols := make([]store.Collection, len(b.Collections))
	copy(cols, b.Collections)
	for i := range cols {
		cols[i].Sidecar = scrubSidecar(cols[i].Sidecar)
	}
	b.Collections = cols
	items := make([]store.Item, len(b.Items))
	copy(items, b.Items)
	for i := range items {
		items[i].Sidecar = scrubSidecar(items[i].Sidecar)
	}
	b.Items = items
}

// scrubSidecar blanks literal credentials in one sidecar document: the
// "value" of a {key|name, value} pair whose key looks like a credential, and
// any string under a credential-named key. {{template}} references stay.
func scrubSidecar(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	changed := false
	v = scrubSidecarValue(v, &changed)
	if !changed {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return raw
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

func literalSecret(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && !strings.Contains(s, "{{")
}

func scrubSidecarValue(v any, changed *bool) any {
	switch t := v.(type) {
	case []any:
		for i := range t {
			t[i] = scrubSidecarValue(t[i], changed)
		}
	case map[string]any:
		pairKey, _ := t["key"].(string)
		if pairKey == "" {
			pairKey, _ = t["name"].(string)
		}
		pairSecret := store.IsSecretName(pairKey)
		for k, e := range t {
			if (pairSecret && k == "value" || store.IsSecretName(k)) && literalSecret(e) {
				t[k] = ""
				*changed = true
				continue
			}
			t[k] = scrubSidecarValue(e, changed)
		}
	}
	return v
}

func bundleHasCollection(b store.CollectionsBundle, uid string) bool {
	for _, col := range b.Collections {
		if col.UID == uid {
			return true
		}
	}
	return false
}

// exportFileStem makes a collection name safe for a header filename.
func exportFileStem(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ', r == '.':
			sb.WriteByte('_')
		}
	}
	s := strings.Trim(sb.String(), "_")
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		s = "collection"
	}
	return s
}

// quoteShort echoes a short, printable form of a client-supplied value.
func quoteShort(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	return `"` + strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == 0x7f {
			return '?'
		}
		return r
	}, s) + `"`
}
