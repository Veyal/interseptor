package mcp

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Collection tools proxy the control API (internal/control/collections*.go).
// Every call carries X-Interseptor-Source: ai (see Server.api), which the
// control plane uses to scrub secrets from reads, force the collection's scope
// policy (default block) on sends, and refuse every trust or capability
// change. There is deliberately no tool that approves scripts: the owner does
// that in the UI.

func pathUID(label, uid string) (string, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return "", errors.New(label + " is required")
	}
	return url.PathEscape(uid), nil
}

func (s *Server) registerCollectionTools() {
	s.add("list_collections",
		"List request collections (Postman-style trees) and environments. Secrets are scrubbed: secret variables show only whether a value exists. Use get_collection for the item tree.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			cols, err := s.apiGet("/api/collections")
			if err != nil {
				return "", err
			}
			envs, err := s.apiGet("/api/environments")
			if err != nil {
				return "", err
			}
			return `{"collections":` + strings.TrimSpace(unwrapField(cols, "collections")) + `,"environments":` + strings.TrimSpace(unwrapField(envs, "environments")) + `}`, nil
		})

	s.add("get_collection",
		"One collection with its items (folders and requests, ordered by rank) and its script approval status. Secrets are scrubbed. Scripts that are not trusted are skipped when requests run; only the owner can trust them in the UI.",
		obj(map[string]any{"collectionUid": p("string", "collection uid from list_collections")}, "collectionUid"),
		func(a map[string]any) (string, error) {
			uid, err := pathUID("collectionUid", argStr(a, "collectionUid"))
			if err != nil {
				return "", err
			}
			tree, err := s.apiGet("/api/collections/" + uid)
			if err != nil {
				return "", err
			}
			status, err := s.apiGet("/api/collections/" + uid + "/scripts")
			if err != nil {
				return "", err
			}
			return `{"tree":` + strings.TrimSpace(tree) + `,"scripts":` + strings.TrimSpace(status) + `}`, nil
		})

	s.add("run_request",
		"Send one collection request through the shared pipeline: variables resolved, trusted scripts run, scope policy applied (the collection's, default block; out-of-scope hosts are refused), and the exchange is captured in History as a flow (flowId in the result). Unresolved {{variables}} block the send. Result lists tests, masked console, variable changes and quarantined scripts.",
		obj(map[string]any{
			"itemUid":   p("string", "request item uid from get_collection"),
			"envUid":    p("string", "environment uid (optional)"),
			"local":     map[string]any{"type": "object", "description": "local variable overrides for this send (string values)"},
			"noScripts": p("boolean", "skip every script"),
			"persist":   p("string", "keep | discard (default discard): whether script variable writes are saved"),
		}, "itemUid"),
		func(a map[string]any) (string, error) {
			body := map[string]any{"itemUid": argStr(a, "itemUid"), "envUid": argStr(a, "envUid"),
				"noScripts": argBool(a, "noScripts", false), "persist": argStr(a, "persist")}
			if l, ok := a["local"].(map[string]any); ok {
				m := map[string]string{}
				for k, v := range l {
					m[k] = argStr(map[string]any{"v": v}, "v")
				}
				body["local"] = m
			}
			return s.api("POST", "/api/collections/send", body)
		})

	s.add("run_collection",
		"Run a collection, a folder or a list of requests sequentially with scope policy block (the AI cannot loosen it). Untrusted scripts are skipped and counted in quarantinedScripts. Returns a summary and one row per request with its flowId; read details with get_flow.",
		obj(map[string]any{
			"collectionUid": p("string", "collection uid"),
			"folderUid":     p("string", "run only this folder"),
			"itemUids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "run only these request uids, in this order"},
			"envUid":        pt("string"),
			"bail":          p("string", "none | on-failure | on-error"),
			"delayMs":       p("integer", "pause between requests, 0-60000"),
			"maxItems":      p("integer", "cap on requests (max 1000)"),
			"noScripts":     pt("boolean"),
			"persist":       p("string", "keep | discard (default discard)"),
		}, "collectionUid"),
		func(a map[string]any) (string, error) {
			body := map[string]any{"collectionUid": argStr(a, "collectionUid"), "folderUid": argStr(a, "folderUid"),
				"envUid": argStr(a, "envUid"), "bail": argStr(a, "bail"), "delayMs": argInt(a, "delayMs", 0),
				"maxItems": argInt(a, "maxItems", 0), "noScripts": argBool(a, "noScripts", false), "persist": argStr(a, "persist")}
			if ids := argStrList(a, "itemUids"); len(ids) > 0 {
				body["itemUids"] = ids
			}
			return s.api("POST", "/api/collections/run", body)
		})

	s.add("set_variable",
		"Set one local current variable value (never the shareable initial value). Declares the variable if it is new. Secret values are stored locally and never echoed back, exported or shown in tool output.",
		obj(map[string]any{
			"ownerKind": p("string", "environment (default) | collection | folder | request | global"),
			"ownerUid":  p("string", "uid of the environment/collection/folder/request (any label for global)"),
			"key":       pt("string"),
			"value":     pt("string"),
			"secret":    p("boolean", "declare as a secret variable when new"),
		}, "ownerUid", "key", "value"),
		func(a map[string]any) (string, error) {
			kind := strings.TrimSpace(argStr(a, "ownerKind"))
			if kind == "" {
				kind = "environment"
			}
			k, err := pathUID("ownerKind", kind)
			if err != nil {
				return "", err
			}
			uid, err := pathUID("ownerUid", argStr(a, "ownerUid"))
			if err != nil {
				return "", err
			}
			return s.api("PUT", "/api/variables/"+k+"/"+uid+"/current", map[string]any{
				"key": argStr(a, "key"), "value": argStr(a, "value"), "secret": argBool(a, "secret", false)})
		})

	s.add("script_approval_status",
		"Read-only: which scripts of a collection are trusted, their analysis (APIs, hosts, risky constructs) and the granted capabilities. There is no tool to trust scripts: ask the owner to review them in the UI.",
		obj(map[string]any{"collectionUid": pt("string")}, "collectionUid"),
		func(a map[string]any) (string, error) {
			uid, err := pathUID("collectionUid", argStr(a, "collectionUid"))
			if err != nil {
				return "", err
			}
			return s.apiGet("/api/collections/" + uid + "/scripts")
		})
}

// unwrapField returns one top-level field of a small JSON document as raw
// JSON; on any surprise it returns the original text so nothing is dropped.
func unwrapField(doc, field string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(doc), &m) != nil || len(m[field]) == 0 {
		return doc
	}
	return string(m[field])
}

// argStrList reads a string array argument.
func argStrList(a map[string]any, key string) []string {
	var out []string
	if arr, ok := a[key].([]any); ok {
		for _, v := range arr {
			if s := strings.TrimSpace(argStr(map[string]any{"v": v}, "v")); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// registerCollmatrixTools proxies the Collections differentiators of
// internal/collmatrix. Like every collection tool they ride the AI channel:
// scope policy block, secrets scrubbed, no script trust.
func (s *Server) registerCollmatrixTools() {
	strList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	post := func(path string, keep ...string) func(map[string]any) (string, error) {
		return func(a map[string]any) (string, error) {
			body := map[string]any{}
			for _, k := range keep {
				if v, ok := a[k]; ok {
					body[k] = v
				}
			}
			return s.api("POST", path, body)
		}
	}
	s.add("collection_identity_matrix",
		"Run a collection (or chosen requests) as each saved authz identity plus anonymous and return the access differential: per request and identity the outcome class, status, size and a flag when an identity expected to be denied succeeded (a hypothesis to reproduce, not proof). Scope policy block; script variable writes are discarded.",
		obj(map[string]any{"collectionUid": pt("string"), "folderUid": pt("string"), "itemUids": strList, "envUid": pt("string"), "identities": strList, "baseline": pt("string"), "noScripts": pt("boolean")}, "collectionUid"),
		post("/api/collmatrix/run", "collectionUid", "folderUid", "itemUids", "envUid", "identities", "baseline", "noScripts"))
	s.add("collection_openapi_coverage",
		"Which operations of an imported OpenAPI spec were exercised by stored runs of a collection: untested, blocked, failing or passing per operation, with statuses seen and a per-tag rollup.",
		obj(map[string]any{"collectionUid": pt("string")}, "collectionUid"),
		func(a map[string]any) (string, error) {
			return s.apiGet("/api/collmatrix/coverage?collection=" + url.QueryEscape(argStr(a, "collectionUid")))
		})
	s.add("collection_intruder_handoff",
		"Build an Intruder attack (target, raw template with section-sign positions, attack type, payloads) from a collection request. Secret variables stay placeholders. Returns the spec; it does not start an attack.",
		obj(map[string]any{"collectionUid": pt("string"), "itemUid": pt("string"), "envUid": pt("string"), "positions": strList, "payloads": strList, "attackType": pt("string"), "dataset": map[string]any{"type": "object", "additionalProperties": strList}}, "collectionUid", "itemUid"),
		post("/api/collmatrix/handoff", "collectionUid", "itemUid", "envUid", "positions", "payloads", "attackType", "dataset"))
	s.add("collection_example_diff",
		"Diff a saved response example of a collection request (name or 1-based index) against a captured response flow: status, headers (volatile ones ignored) and JSON structure by path. ignorePaths accepts $.items[*].id style paths.",
		obj(map[string]any{"collectionUid": pt("string"), "itemUid": pt("string"), "example": pt("string"), "flowId": pt("integer"), "ignorePaths": strList, "ignoreHeaders": strList}, "collectionUid", "itemUid", "flowId"),
		post("/api/collmatrix/diff-example", "collectionUid", "itemUid", "example", "flowId", "ignorePaths", "ignoreHeaders"))
	s.add("collection_run_timing",
		"Timing breakdown of a stored collection run: wall time split into requests, tests and other; per-request min, median, p95, max; slow outliers. Request time is the total round trip.",
		obj(map[string]any{"collectionUid": pt("string"), "runUid": pt("string")}, "collectionUid", "runUid"),
		func(a map[string]any) (string, error) {
			return s.apiGet("/api/collmatrix/timing?collection=" + url.QueryEscape(argStr(a, "collectionUid")) + "&run=" + url.QueryEscape(argStr(a, "runUid")))
		})
	s.add("collection_attach_run_evidence",
		"Attach the captured flows of a stored collection run (or selected requests) to a finding as typed evidence with a run-context note. Secrets are masked in the notes.",
		obj(map[string]any{"collectionUid": pt("string"), "runUid": pt("string"), "itemUids": strList, "findingId": pt("integer")}, "collectionUid", "runUid", "findingId"),
		post("/api/collmatrix/attach-run", "collectionUid", "runUid", "itemUids", "findingId"))
}
