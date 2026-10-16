package collmatrix

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Veyal/interseptor/internal/collexec"
)

// Tool is an MCP tool descriptor. The mcp package adapts these to its own
// registry (kept out of here so collmatrix never imports mcp). Every call is
// an AI call: scope policy block, no secrets in results, no script trust.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

var (
	strProp  = map[string]any{"type": "string"}
	intProp  = map[string]any{"type": "integer"}
	boolProp = map[string]any{"type": "boolean"}
	strList  = map[string]any{"type": "array", "items": strProp}
)

func parseArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return errors.New("bad arguments: " + err.Error())
	}
	return nil
}

// Tools returns the MCP tools of the package.
func (s *Service) Tools() []Tool {
	return []Tool{
		{
			Name:        "collection_identity_matrix",
			Description: "Run a collection (or chosen requests) as each authz identity plus anonymous and return the access differential: per request and identity the outcome class, status, size and a flag when an identity expected to be denied succeeded (a hypothesis to reproduce, not proof). Uses the scope policy block; script variable writes are discarded.",
			InputSchema: obj(map[string]any{"collectionUid": strProp, "folderUid": strProp, "itemUids": strList, "envUid": strProp, "identities": strList, "baseline": strProp, "noScripts": boolProp}, "collectionUid"),
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var req MatrixRequest
				if err := parseArgs(a, &req); err != nil {
					return nil, err
				}
				req.AI, req.Source, req.ScopePolicy, req.Local = true, collexec.SourceMCP, "", nil
				m, err := s.RunMatrix(ctx, req)
				if err != nil {
					return nil, err
				}
				return map[string]any{"matrix": m, "timing": TimingDifferential(m), "timingNote": TimingNote}, nil
			},
		},
		{
			Name:        "collection_openapi_coverage",
			Description: "Report which operations of an imported OpenAPI spec were exercised by stored runs of a collection: untested, blocked, failing or passing per operation, with the HTTP statuses seen and a per-tag rollup.",
			InputSchema: obj(map[string]any{"collectionUid": strProp}, "collectionUid"),
			Call: func(_ context.Context, a json.RawMessage) (any, error) {
				var in struct {
					CollectionUID string `json:"collectionUid"`
				}
				if err := parseArgs(a, &in); err != nil {
					return nil, err
				}
				return s.CoverageFor(in.CollectionUID)
			},
		},
		{
			Name:        "collection_intruder_handoff",
			Description: "Build an Intruder attack (target, raw template with section-sign positions, attack type, payloads) from a collection request. Positions default to variables in the path, query and body. Secret variables stay placeholders. Returns the spec; it does not start an attack.",
			InputSchema: obj(map[string]any{"collectionUid": strProp, "itemUid": strProp, "envUid": strProp, "positions": strList, "payloads": strList, "attackType": strProp, "dataset": map[string]any{"type": "object", "additionalProperties": strList}}, "collectionUid", "itemUid"),
			Call: func(_ context.Context, a json.RawMessage) (any, error) {
				var in struct {
					CollectionUID string `json:"collectionUid"`
					ItemUID       string `json:"itemUid"`
					EnvUID        string `json:"envUid"`
					HandoffOptions
				}
				if err := parseArgs(a, &in); err != nil {
					return nil, err
				}
				return s.Handoff(in.CollectionUID, in.ItemUID, in.EnvUID, in.HandoffOptions, false)
			},
		},
		{
			Name:        "collection_example_diff",
			Description: "Diff a saved response example of a collection request (by name or 1-based index) against a captured response flow: status, headers (volatile ones ignored), and JSON structure by path. ignorePaths accepts $.items[*].id style paths.",
			InputSchema: obj(map[string]any{"collectionUid": strProp, "itemUid": strProp, "example": strProp, "flowId": intProp, "ignorePaths": strList, "ignoreHeaders": strList}, "collectionUid", "itemUid", "flowId"),
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var in struct {
					CollectionUID string `json:"collectionUid"`
					ItemUID       string `json:"itemUid"`
					Example       string `json:"example"`
					FlowID        int64  `json:"flowId"`
					DiffOptions
				}
				if err := parseArgs(a, &in); err != nil {
					return nil, err
				}
				return s.DiffExample(ctx, in.CollectionUID, in.ItemUID, in.Example, in.FlowID, in.DiffOptions)
			},
		},
		{
			Name:        "collection_run_timing",
			Description: "Timing breakdown of a stored collection run: wall time split into requests, tests and other; per-request min, median, p95, max; slow outliers. Request time is the total round trip (no DNS/connect/TLS split is recorded).",
			InputSchema: obj(map[string]any{"collectionUid": strProp, "runUid": strProp}, "collectionUid", "runUid"),
			Call: func(_ context.Context, a json.RawMessage) (any, error) {
				var in struct {
					CollectionUID string `json:"collectionUid"`
					RunUID        string `json:"runUid"`
				}
				if err := parseArgs(a, &in); err != nil {
					return nil, err
				}
				return s.TimingForRun(in.CollectionUID, in.RunUID)
			},
		},
		{
			Name:        "collection_attach_run_evidence",
			Description: "Attach the captured flows of a stored collection run (or selected requests of it) to a finding as typed evidence, each with a run-context note (run, request, env, status, failed test). Secrets are masked in the notes.",
			InputSchema: obj(map[string]any{"collectionUid": strProp, "runUid": strProp, "itemUids": strList, "findingId": intProp}, "collectionUid", "runUid", "findingId"),
			Call: func(_ context.Context, a json.RawMessage) (any, error) {
				var in struct {
					CollectionUID string   `json:"collectionUid"`
					RunUID        string   `json:"runUid"`
					ItemUIDs      []string `json:"itemUids"`
					FindingID     int64    `json:"findingId"`
				}
				if err := parseArgs(a, &in); err != nil {
					return nil, err
				}
				n, err := s.AttachRun(in.CollectionUID, in.RunUID, in.ItemUIDs, in.FindingID)
				return map[string]any{"attached": n}, err
			},
		},
	}
}
