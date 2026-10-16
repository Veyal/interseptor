package control

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/Veyal/interseptor/internal/version"
)

// This file generates the OpenAPI 3.1 document served at GET /openapi.json.
//
// It is generated from apiRoutes — the same catalog GET /api/reference and the
// published REST table come from — so the document cannot drift from the
// surface it describes. What it declares per operation is everything the route
// catalog actually knows: method, path, templated path parameters, a summary, a
// tag, the catalog description, and a response. What the catalog does not hold
// in machine-readable form — per-field request and response schemas, and query
// parameters — is not invented here: a generated SDK gets one correctly routed
// method per operation with an untyped JSON body and result, and the field
// lists stay in the operation descriptions where the catalog keeps them.
//
// The previous payload at this URL was a bespoke index (`kind`,
// `interseptor-route-index`). Its route array is preserved as the
// x-interseptor-routes extension; GET /api/reference serves the same data in
// the old shape.

const openapiVersion = "3.1.1"

// openapiDescription is info.description. It states, in the document itself,
// exactly how far the declarations go, so nobody generating a client has to
// discover the limits by hitting them.
const openapiDescription = "REST control API of Interseptor, an HTTP/HTTPS intercepting proxy and pentest workspace. " +
	"Generated from the server's route catalog, so every route the running build serves is present with its real " +
	"method, path, path parameters and operation id.\n\n" +
	"Scope of the declarations: request and response bodies are declared permissively (an untyped JSON object), and " +
	"query parameters are not declared at all. The catalog holds those as prose, and each operation's `description` " +
	"is that prose verbatim — it lists the accepted body fields and query parameters. A generated SDK therefore gives " +
	"you one correctly routed method per operation; read the description for the payload. Routes that return an image, " +
	"Markdown or an event stream declare no response schema rather than claiming JSON.\n\n" +
	"The API is loopback-trust by default. Once API keys exist, send `Authorization: Bearer <key>`."

// nonIdentifier is every character that must not reach a generated method name,
// plus the underscore runs collapsing them would leave behind.
var nonIdentifier = regexp.MustCompile(`[^a-z0-9]+`)

// pathTemplate matches an OpenAPI path template variable.
var pathTemplate = regexp.MustCompile(`\{([^}/]+)\}`)

// buildOpenAPI produces the OpenAPI 3.1 document for the live route catalog.
func buildOpenAPI(baseURL string) map[string]any {
	paths := map[string]any{}
	tags := map[string]bool{}
	for _, r := range apiRoutes {
		if r.Path == "" || r.Path == "/" {
			continue // the SPA catch-all is not part of the API
		}
		tag := routeTag(r.Path)
		tags[tag] = true
		item, ok := paths[r.Path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[r.Path] = item
		}
		item[strings.ToLower(r.Method)] = operation(r, tag)
	}
	return map[string]any{
		"openapi": openapiVersion,
		"info": map[string]any{
			"title":       "Interseptor control API",
			"version":     version.String(),
			"description": openapiDescription,
			"license":     map[string]any{"name": "MIT", "identifier": "MIT"},
		},
		"servers":    []any{map[string]any{"url": baseURL, "description": "The running Interseptor control plane"}},
		"tags":       tagObjects(tags),
		"paths":      paths,
		"components": openapiComponents(),
		// Compatibility: the bespoke index this URL used to return had a
		// `routes` array of {method, path, desc}. Keep it as an extension so a
		// reader of the old payload keeps working.
		"x-interseptor-routes": routeExtension(),
	}
}

// operation builds one OpenAPI operation from a catalog route.
func operation(r apiRoute, tag string) map[string]any {
	op := map[string]any{
		"operationId": opID(r.Method, r.Path),
		"summary":     routeSummary(r.Desc, r.Method, r.Path),
		"description": r.Desc,
		"tags":        []any{tag},
		"responses":   responses(r),
	}
	if params := pathParameters(r.Path); len(params) > 0 {
		op["parameters"] = params
	}
	if methodTakesBody(r.Method) {
		op["requestBody"] = requestBody()
	}
	return op
}

// methodTakesBody reports whether a method carries a request body here. The
// catalog has no GET or DELETE route that reads one.
func methodTakesBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// requestBody is the permissive JSON body. It is never required: several POST
// routes take no body at all, and the catalog does not record which.
func requestBody() map[string]any {
	return map[string]any{
		"required":    false,
		"description": "JSON object. The accepted fields are listed in this operation's description; a few routes take a raw file upload instead and say so there.",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/JSONObject"},
			},
		},
	}
}

// responses declares the success response plus the errors the control plane's
// shared helpers can return from any route.
func responses(r apiRoute) map[string]any {
	success := map[string]any{
		"description": "Success. The response shape is described in this operation's description.",
	}
	if !returnsNonJSON(r.Path, r.Desc) {
		success["content"] = map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/JSONValue"},
			},
		}
	} else {
		success["description"] = "Success. This route does not return JSON — see this operation's description for the payload."
	}
	return map[string]any{
		"200":     success,
		"400":     map[string]any{"$ref": "#/components/responses/BadRequest"},
		"401":     map[string]any{"$ref": "#/components/responses/Unauthorized"},
		"500":     map[string]any{"$ref": "#/components/responses/ServerError"},
		"default": map[string]any{"$ref": "#/components/responses/Error"},
	}
}

// nonJSONMarkers are the signals, in a route's path or catalog description,
// that its success payload is not JSON. The list errs toward "not JSON": a
// false positive costs a response schema the caller can read from the
// description anyway, while a false negative would put a lie in the document.
var nonJSONMarkers = []string{
	"markdown", "png", "server-sent events", "raw bytes", "body bytes",
	".tar.gz", "zip archive", "for download", "image response",
}

// nonJSONPathMarkers are path shapes whose payload is never JSON.
var nonJSONPathMarkers = []string{".png", "/raw", "/body", "/events", "/reference", "/render"}

// returnsNonJSON reports whether a route's success payload is something other
// than JSON, so the document declares no JSON schema for it.
func returnsNonJSON(path, desc string) bool {
	lowerPath := strings.ToLower(path)
	for _, m := range nonJSONPathMarkers {
		if strings.HasSuffix(lowerPath, m) || strings.Contains(lowerPath, m+"?") {
			return true
		}
	}
	lowerDesc := strings.ToLower(desc)
	for _, m := range nonJSONMarkers {
		if strings.Contains(lowerDesc, m) {
			return true
		}
	}
	return false
}

// pathParameters declares every {template} variable in a path. OpenAPI
// requires this: a path item with an undeclared template variable is invalid
// and generators emit broken clients for it. The catalog does not record
// whether an id is numeric or a name, so every one is a string — which is what
// it is on the wire.
func pathParameters(path string) []any {
	var out []any
	for _, m := range pathTemplate.FindAllStringSubmatch(path, -1) {
		out = append(out, map[string]any{
			"name":        m[1],
			"in":          "path",
			"required":    true,
			"description": "Path segment " + m[1] + ", as it appears on the wire.",
			"schema":      map[string]any{"type": "string"},
		})
	}
	return out
}

// openapiComponents holds the shared schemas and error responses. The two
// schemas are deliberately permissive — see the file comment.
func openapiComponents() map[string]any {
	errSchema := map[string]any{
		"type":        "object",
		"description": "Error payload written by the control plane's shared error helpers.",
		"properties": map[string]any{
			"error": map[string]any{"type": "string", "description": "Human-readable reason."},
		},
	}
	errResponse := func(desc string) map[string]any {
		return map[string]any{
			"description": desc,
			"content": map[string]any{
				"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}},
			},
		}
	}
	return map[string]any{
		"schemas": map[string]any{
			"JSONObject": map[string]any{
				"type":                 "object",
				"description":          "A JSON object whose fields are documented in prose on the operation that accepts it.",
				"additionalProperties": true,
			},
			"JSONValue": map[string]any{
				"description": "A JSON value whose shape is documented in prose on the operation that returns it.",
			},
			"Error": errSchema,
		},
		"responses": map[string]any{
			"BadRequest":   errResponse("The request was rejected: a missing or invalid argument, or a body over the route's size cap."),
			"Unauthorized": errResponse("An API key exists and the request did not carry a valid Authorization: Bearer credential."),
			"ServerError":  errResponse("The server failed to complete the request."),
			"Error":        errResponse("An error the operation's description does not single out."),
		},
		"securitySchemes": map[string]any{
			"bearerAuth": map[string]any{
				"type":        "http",
				"scheme":      "bearer",
				"description": "An Interseptor API key, created in Settings → API & MCP. Optional: the control plane is loopback-trust until a key exists.",
			},
		},
	}
}

// routeExtension is the legacy route array, preserved as an x- extension.
func routeExtension() []any {
	out := make([]any, 0, len(apiRoutes))
	for _, r := range apiRoutes {
		out = append(out, map[string]any{"method": r.Method, "path": r.Path, "desc": r.Desc})
	}
	return out
}

// routeTag groups a route by its first meaningful path segment, so a generated
// client has one class per area instead of 284 loose methods.
func routeTag(path string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/"), "api/")
	seg := trimmed
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	seg = strings.TrimSuffix(seg, ".json")
	if seg == "" {
		return "meta"
	}
	return seg
}

// tagObjects renders the collected tags as a sorted OpenAPI tags array.
func tagObjects(tags map[string]bool) []any {
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name, "description": "Routes under /api/" + name + "."})
	}
	return out
}

// routeSummary reduces a catalog description to a one-line summary. The catalog
// descriptions are long on purpose — they carry the field lists — so the first
// sentence becomes the summary and the whole text stays as the description.
func routeSummary(desc, method, path string) string {
	s := strings.TrimSpace(desc)
	if s == "" {
		return strings.ToUpper(method) + " " + path
	}
	// Cut at the first sentence end that is not inside a {…} field list.
	if i := strings.IndexAny(s, ".;:("); i > 0 && !strings.Contains(s[:i], "{") {
		s = strings.TrimSpace(s[:i])
	}
	const max = 120
	if len(s) > max {
		s = strings.TrimSpace(s[:max])
	}
	if s == "" {
		return strings.ToUpper(method) + " " + path
	}
	return s
}

// opID is the stable operation id for a route: the method plus the whole path,
// template braces dropped and non-identifier characters collapsed so the id is
// usable as a generated method name. The whole path is used — not the path with
// /api/ stripped — because stripping it collides /mcp with /api/mcp, and a
// duplicate operationId makes a generated client drop one of the two routes.
func opID(method, path string) string {
	id := strings.ToLower(method) + "_" + strings.ToLower(path)
	id = nonIdentifier.ReplaceAllString(id, "_")
	return strings.Trim(id, "_")
}

func (h *metaAPI) openapi(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildOpenAPI("http://"+r.Host))
}
