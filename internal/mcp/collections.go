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
