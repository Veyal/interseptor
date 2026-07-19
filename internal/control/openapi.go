package control

import (
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/version"
)

// buildRouteIndex produces a machine-readable discovery index from apiRoutes.
// It deliberately does not claim to be OpenAPI: request/response schemas and
// parameter declarations live in the prose descriptions, so client generation
// would be unsafe. The legacy /openapi.json URL is retained for compatibility.
func buildRouteIndex(baseURL string) map[string]any {
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
		"kind":        "interseptor-route-index",
		"version":     version.String(),
		"description": "REST route discovery index (method, path, summary); not an OpenAPI contract or client-generation schema.",
		"baseURL":     baseURL,
		"routes":      apiRoutes,
		"paths":       paths,
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
	writeJSON(w, http.StatusOK, buildRouteIndex("http://"+r.Host))
}
