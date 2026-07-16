package control

import (
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/version"
)

// buildOpenAPI produces an OpenAPI 3.1 document from the hand-maintained apiRoutes
// catalog. It's a structural map of every REST endpoint (method + path + summary)
// so tooling can generate clients, Postman collections, or typed SDKs without
// reading handler source. Request/response bodies aren't schema'd here — the
// catalog is prose-described; this is the route surface, not a full contract.
func buildOpenAPI(baseURL string) map[string]any {
	paths := map[string]any{}
	for _, r := range apiRoutes {
		path := openapiPath(r.Path)
		op := map[string]any{"summary": r.Desc, "operationId": opID(r.Method, r.Path)}
		entry, ok := paths[path].(map[string]any)
		if !ok {
			entry = map[string]any{}
			paths[path] = entry
		}
		entry[strings.ToLower(r.Method)] = op
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Interseptor",
			"version":     version.String(),
			"description": "Intercepting HTTP/HTTPS proxy + security toolkit. Same engine the UI and the MCP server drive.",
		},
		"servers": []map[string]any{{"url": baseURL}},
		"paths":   paths,
	}
}

// openapiPath leaves {id}-style path params as-is (already OpenAPI-shaped) and
// leaves plain paths untouched.
func openapiPath(p string) string { return p }

func opID(method, path string) string {
	clean := strings.NewReplacer("/api/", "", "{", "", "}", "", "/", "_").Replace(path)
	return strings.ToLower(method) + "_" + clean
}

func (h *metaAPI) openapi(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildOpenAPI("http://"+r.Host))
}
