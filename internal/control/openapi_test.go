package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// jsonSchemaTypes are the only type values JSON Schema 2020-12 — and therefore
// OpenAPI 3.1 — accepts.
var jsonSchemaTypes = []string{"null", "boolean", "object", "array", "number", "string", "integer"}

func openapiDoc(t *testing.T) map[string]any {
	t.Helper()
	// Round-trip through JSON so the test sees exactly what a client sees:
	// map[string]any everywhere, no Go-only types smuggled through.
	raw, err := json.Marshal(buildOpenAPI("http://127.0.0.1:9966"))
	if err != nil {
		t.Fatalf("OpenAPI document is not marshalable: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("OpenAPI document is not valid JSON: %v", err)
	}
	return doc
}

// The document must satisfy the parts of OpenAPI 3.1 that a generator relies
// on: a 3.1 version string, info.title, info.version, servers, and paths.
func TestOpenAPIDocumentHasTheRequiredRootFields(t *testing.T) {
	doc := openapiDoc(t)
	version, _ := doc["openapi"].(string)
	if !strings.HasPrefix(version, "3.1") {
		t.Fatalf("openapi = %q, want a 3.1.x version", doc["openapi"])
	}
	info, ok := doc["info"].(map[string]any)
	if !ok {
		t.Fatal("info object missing")
	}
	for _, key := range []string{"title", "version", "description"} {
		if s, _ := info[key].(string); s == "" {
			t.Errorf("info.%s is empty", key)
		}
	}
	servers, ok := doc["servers"].([]any)
	if !ok || len(servers) == 0 {
		t.Fatalf("servers = %#v, want at least one entry", doc["servers"])
	}
	first, _ := servers[0].(map[string]any)
	if first["url"] != "http://127.0.0.1:9966" {
		t.Errorf("servers[0].url = %v, want the request base URL", first["url"])
	}
	if _, ok := doc["paths"].(map[string]any); !ok {
		t.Fatal("paths object missing")
	}
	// A generated SDK needs to know which routes are grouped together.
	tags, ok := doc["tags"].([]any)
	if !ok || len(tags) == 0 {
		t.Fatalf("tags = %#v, want the route groups", doc["tags"])
	}
}

// Every route in the catalog must appear, so the document cannot drift from
// the REST surface it describes.
func TestOpenAPICoversEveryCatalogRoute(t *testing.T) {
	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)

	for _, r := range apiRoutes {
		item, ok := paths[r.Path].(map[string]any)
		if !ok {
			t.Fatalf("path %q is missing from the OpenAPI document", r.Path)
		}
		op, ok := item[strings.ToLower(r.Method)].(map[string]any)
		if !ok {
			t.Fatalf("%s %s is missing from the OpenAPI document", r.Method, r.Path)
		}
		if s, _ := op["summary"].(string); s == "" {
			t.Errorf("%s %s has no summary", r.Method, r.Path)
		}
		if s, _ := op["description"].(string); s != r.Desc {
			t.Errorf("%s %s description is not the catalog description", r.Method, r.Path)
		}
	}
	if _, leaked := paths["/"]; leaked {
		t.Error("the SPA catch-all / leaked into the OpenAPI paths")
	}
	// Path count: one entry per distinct path in the catalog.
	distinct := map[string]bool{}
	for _, r := range apiRoutes {
		distinct[r.Path] = true
	}
	if len(paths) != len(distinct) {
		t.Errorf("paths = %d entries, catalog has %d distinct paths", len(paths), len(distinct))
	}
}

// Every path item must carry at least one operation, every operation an
// operationId and a responses object, and every operationId must be unique —
// generators key their method names off it.
func TestOpenAPIOperationsAreCompleteAndUniquelyIdentified(t *testing.T) {
	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)
	methods := []string{"get", "put", "post", "delete", "patch", "options", "head", "trace"}
	idSafe := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	seen := map[string]string{}
	for path, raw := range paths {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("path item for %q is not an object", path)
		}
		var ops int
		for key, rawOp := range item {
			if !slices.Contains(methods, key) {
				t.Errorf("%s has a non-operation key %q", path, key)
				continue
			}
			ops++
			op := rawOp.(map[string]any)
			id, _ := op["operationId"].(string)
			if !idSafe.MatchString(id) {
				t.Errorf("%s %s operationId = %q, not a generator-safe identifier", key, path, id)
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("operationId %q is used by both %s and %s %s", id, prev, key, path)
			}
			seen[id] = key + " " + path
			responses, ok := op["responses"].(map[string]any)
			if !ok || len(responses) == 0 {
				t.Errorf("%s %s has no responses", key, path)
				continue
			}
			for code, rawResp := range responses {
				resp, ok := rawResp.(map[string]any)
				if !ok {
					t.Errorf("%s %s response %s is not an object", key, path, code)
					continue
				}
				if _, isRef := resp["$ref"]; isRef {
					continue
				}
				if s, _ := resp["description"].(string); s == "" {
					t.Errorf("%s %s response %s has no description", key, path, code)
				}
			}
			if _, ok := responses["200"]; !ok {
				if _, ok := responses["204"]; !ok {
					t.Errorf("%s %s declares neither a 200 nor a 204 response", key, path)
				}
			}
			if tags, ok := op["tags"].([]any); !ok || len(tags) == 0 {
				t.Errorf("%s %s has no tags", key, path)
			}
		}
		if ops == 0 {
			t.Errorf("path %q has no operations", path)
		}
	}
}

// OpenAPI requires every {template} variable in a path to be declared as a
// required path parameter. A document that skips this fails validation and
// generators produce broken clients.
func TestOpenAPIDeclaresEveryPathTemplateVariable(t *testing.T) {
	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)
	tmpl := regexp.MustCompile(`\{([^}/]+)\}`)

	var checked int
	for path, raw := range paths {
		want := tmpl.FindAllStringSubmatch(path, -1)
		if len(want) == 0 {
			continue
		}
		checked++
		for key, rawOp := range raw.(map[string]any) {
			op := rawOp.(map[string]any)
			params, _ := op["parameters"].([]any)
			declared := map[string]bool{}
			for _, rawParam := range params {
				p := rawParam.(map[string]any)
				if p["in"] != "path" {
					continue
				}
				if p["required"] != true {
					t.Errorf("%s %s path parameter %v is not required", key, path, p["name"])
				}
				schema, ok := p["schema"].(map[string]any)
				if !ok {
					t.Errorf("%s %s path parameter %v has no schema", key, path, p["name"])
					continue
				}
				if typ, _ := schema["type"].(string); !slices.Contains(jsonSchemaTypes, typ) {
					t.Errorf("%s %s path parameter %v schema type = %q, not a JSON Schema type", key, path, p["name"], typ)
				}
				declared[p["name"].(string)] = true
			}
			for _, m := range want {
				if !declared[m[1]] {
					t.Errorf("%s %s does not declare path parameter %q", key, path, m[1])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no templated paths were checked; the catalog should have {id} routes")
	}
}

// Every `type` anywhere in the document must be a JSON Schema 2020-12 type.
// A stray "int" or "bool" is the classic way a hand-rolled spec fails
// validation only once someone runs a generator over it.
func TestOpenAPISchemaTypesAreValid(t *testing.T) {
	doc := openapiDoc(t)
	var walk func(path string, v any)
	var typesSeen int
	walk = func(at string, v any) {
		switch node := v.(type) {
		case map[string]any:
			for key, child := range node {
				// securitySchemes.*.type is a Security Scheme Object type
				// ("http", "apiKey", …), a different enum from JSON Schema's.
				if key == "securitySchemes" {
					continue
				}
				if key == "type" {
					if s, ok := child.(string); ok {
						typesSeen++
						if !slices.Contains(jsonSchemaTypes, s) {
							t.Errorf("%s.type = %q is not a JSON Schema type", at, s)
						}
						continue
					}
					// 3.1 allows a type array; every member must still be valid.
					if arr, ok := child.([]any); ok {
						for _, m := range arr {
							typesSeen++
							s, _ := m.(string)
							if !slices.Contains(jsonSchemaTypes, s) {
								t.Errorf("%s.type contains %q, not a JSON Schema type", at, s)
							}
						}
						continue
					}
					t.Errorf("%s.type = %#v is neither a string nor an array", at, child)
				}
				walk(at+"."+key, child)
			}
		case []any:
			for i, child := range node {
				walk(at, child)
				_ = i
			}
		}
	}
	walk("$", doc)
	if typesSeen == 0 {
		t.Fatal("the document declares no schema types at all")
	}
}

// Bodies: a method that can carry one declares an optional JSON requestBody so
// a generated client has somewhere to put it; a GET must not.
func TestOpenAPIDeclaresRequestBodiesOnlyWhereTheyApply(t *testing.T) {
	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)
	var withBody int
	for path, raw := range paths {
		for key, rawOp := range raw.(map[string]any) {
			op := rawOp.(map[string]any)
			body, has := op["requestBody"].(map[string]any)
			switch key {
			case "get", "delete", "head", "options":
				if has {
					t.Errorf("%s %s declares a requestBody", key, path)
				}
			case "post", "put", "patch":
				if !has {
					t.Errorf("%s %s declares no requestBody", key, path)
					continue
				}
				withBody++
				if body["required"] == true {
					t.Errorf("%s %s marks its body required; the catalog does not say that", key, path)
				}
				content, ok := body["content"].(map[string]any)
				if !ok || content["application/json"] == nil {
					t.Errorf("%s %s requestBody has no application/json content: %#v", key, path, body["content"])
				}
			}
		}
	}
	if withBody == 0 {
		t.Fatal("no request bodies were declared")
	}
}

// The document must not claim a JSON response for a route that returns an
// image, Markdown or an event stream. Those declare a description only.
func TestOpenAPIDoesNotClaimJSONForNonJSONRoutes(t *testing.T) {
	doc := openapiDoc(t)
	paths := doc["paths"].(map[string]any)

	content := func(path, method string) map[string]any {
		t.Helper()
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("%s is not in the document", path)
		}
		op, ok := item[method].(map[string]any)
		if !ok {
			t.Fatalf("%s %s is not in the document", method, path)
		}
		resp := op["responses"].(map[string]any)["200"].(map[string]any)
		c, _ := resp["content"].(map[string]any)
		return c
	}

	for _, path := range []string{"/api/events", "/api/scanner/report", "/api/flows/{id}/preview.png", "/api/flows/{id}/body", "/api/checks/reference"} {
		if c := content(path, "get"); c["application/json"] != nil {
			t.Errorf("GET %s claims a JSON response body", path)
		}
	}
	for _, path := range []string{"/api/flows", "/api/version"} {
		if c := content(path, "get"); c["application/json"] == nil {
			t.Errorf("GET %s does not declare its JSON response", path)
		}
	}
}

// The bespoke route index that /openapi.json used to return is kept as an
// x- extension, so anything that read `routes` still works and the removal of
// the old top-level keys is the only breaking part.
func TestOpenAPIKeepsTheLegacyRouteArrayAsAnExtension(t *testing.T) {
	doc := openapiDoc(t)
	routes, ok := doc["x-interseptor-routes"].([]any)
	if !ok || len(routes) != len(apiRoutes) {
		t.Fatalf("x-interseptor-routes = %d entries, catalog has %d", len(routes), len(apiRoutes))
	}
	first := routes[0].(map[string]any)
	for _, key := range []string{"method", "path", "desc"} {
		if _, ok := first[key]; !ok {
			t.Errorf("x-interseptor-routes entry is missing %q: %#v", key, first)
		}
	}
	// The old payload's top-level keys are gone; GET /api/reference replaces
	// them. Nothing may claim this is still a route index.
	for _, gone := range []string{"kind", "routes", "baseURL"} {
		if _, present := doc[gone]; present {
			t.Errorf("legacy top-level key %q is still present", gone)
		}
	}
}

// GET /openapi.json must serve the document over HTTP with the right content
// type and the requesting host as the server URL.
func TestOpenAPIEndpointServesTheDocument(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1") {
		t.Fatalf("served openapi = %q", doc["openapi"])
	}
	servers := doc["servers"].([]any)
	if url, _ := servers[0].(map[string]any)["url"].(string); !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("servers[0].url = %q, want the requested host", url)
	}
}

func TestOpID(t *testing.T) {
	cases := map[[2]string]string{
		{"GET", "/api/checks/{id}"}:          "get_api_checks_id",
		{"GET", "/api/render/flow-diff.png"}: "get_api_render_flow_diff_png",
		{"GET", "/openapi.json"}:             "get_openapi_json",
		// /mcp and /api/mcp are different routes and must not collide.
		{"GET", "/mcp"}:     "get_mcp",
		{"GET", "/api/mcp"}: "get_api_mcp",
	}
	for in, want := range cases {
		if got := opID(in[0], in[1]); got != want {
			t.Errorf("opID(%s %s) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
