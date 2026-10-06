package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

func findingRevisionArg(a map[string]any, key string, optional bool) (int64, error) {
	value := a[key]
	if optional && (value == nil || value == "") {
		return 0, nil
	}
	var id int64
	var err error
	switch v := value.(type) {
	case int:
		id = int64(v)
	case int64:
		id = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v >= 0x1p63 {
			return 0, fmt.Errorf("invalid %s: expected an integer in range", key)
		}
		id = int64(v)
	case string:
		id, err = strconv.ParseInt(v, 10, 64)
	case json.Number:
		id, err = v.Int64()
	default:
		return 0, fmt.Errorf("invalid %s: expected an integer", key)
	}
	if err != nil || id < 0 || (!optional && id == 0) {
		if optional {
			return 0, fmt.Errorf("invalid %s: expected a nonnegative integer", key)
		}
		return 0, fmt.Errorf("invalid %s: expected a positive integer", key)
	}
	return id, nil
}

func (s *Server) registerFindingReviewTools() {
	s.add("finding_readiness", "Project-wide report readiness board: one row per finding (id, title, severity, status, ready, blocking gaps) sorted by severity, plus per-finding final-gate issues {rule, field, capability, message}. Pass id for one finding's gate result. Same results as the UI and API; these assess completeness, not independent exploit verification. Statuses default to open,verified,fixed; use statuses=all for every finding.", obj(map[string]any{"statuses": pt("string"), "tag": pt("string"), "id": pt("integer")}), func(a map[string]any) (string, error) {
		if a["id"] != nil && a["id"] != "" {
			id, err := findingRevisionArg(a, "id", false)
			if err != nil {
				return "", err
			}
			return s.apiGet(fmt.Sprintf("/api/finding-quality/%d", id))
		}
		q := url.Values{}
		q.Set("statuses", argStr(a, "statuses"))
		q.Set("tag", argStr(a, "tag"))
		return s.apiGet("/api/findings/readiness?" + q.Encode())
	})
	s.add("list_finding_revisions", "List immutable finding revision metadata, including deleted findings. Use before for older pages. No raw evidence in the list.", obj(map[string]any{"id": pt("integer"), "before": pt("integer")}, "id"), func(a map[string]any) (string, error) {
		id, err := findingRevisionArg(a, "id", false)
		if err != nil {
			return "", err
		}
		before, err := findingRevisionArg(a, "before", true)
		if err != nil {
			return "", err
		}
		return s.apiGet(fmt.Sprintf("/api/finding-revisions/%d?before=%d", id, before))
	})
	s.add("get_finding_revision", "Read one historical finding snapshot and its field-level diff. May contain sensitive recorded evidence; handle like the current finding.", obj(map[string]any{"id": pt("integer"), "revisionId": pt("integer")}, "id", "revisionId"), func(a map[string]any) (string, error) {
		id, err := findingRevisionArg(a, "id", false)
		if err != nil {
			return "", err
		}
		revisionID, err := findingRevisionArg(a, "revisionId", false)
		if err != nil {
			return "", err
		}
		return s.apiGet(fmt.Sprintf("/api/finding-revisions/%d/%d", id, revisionID))
	})
	s.add("restore_finding_revision", "Restore a selected historical finding version, including a deleted finding. Appends a new revision; separately purged traffic remains missing. Obtain the operator's intent before replacing current report content.", obj(map[string]any{"id": pt("integer"), "revisionId": pt("integer"), "reason": pt("string")}, "id", "revisionId"), func(a map[string]any) (string, error) {
		id, err := findingRevisionArg(a, "id", false)
		if err != nil {
			return "", err
		}
		revisionID, err := findingRevisionArg(a, "revisionId", false)
		if err != nil {
			return "", err
		}
		return s.api(http.MethodPost, fmt.Sprintf("/api/finding-revisions/%d/%d/restore", id, revisionID), map[string]any{"reason": argStr(a, "reason")})
	})
	s.add("preview_finding_targets", "Preview exact target deduplication and optional path templates without saving. Evidence links are retained. Apply templates only after reviewer approval through update_finding.", obj(map[string]any{"targets": findingTargetsSchema(), "legacy": pt("string")}), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, "/api/finding-targets/preview", a)
	})
	s.add("normalize_finding_targets", "Normalize a saved finding's affected targets with reviewer-approved path templates (for example /users/edit/123 -> /users/edit/{id}). First call preview_finding_targets to list suggestions, then pass the approved suggestion indexes in approve. dryRun defaults to true (no changes); set dryRun=false to persist. Targets that differ in method, scheme, role, relation or variant are never merged; flow and image references stay on their target.", obj(map[string]any{"id": pt("integer"), "approve": map[string]any{"type": "array", "items": pt("integer"), "description": "suggestion indexes (target positions) approved for templating"}, "dryRun": p("boolean", "default true; false persists the normalized targets")}, "id"), func(a map[string]any) (string, error) {
		id, err := findingRevisionArg(a, "id", false)
		if err != nil {
			return "", err
		}
		return s.api(http.MethodPost, fmt.Sprintf("/api/findings/%d/normalize-targets", id), map[string]any{"approve": a["approve"], "dryRun": argBool(a, "dryRun", true)})
	})
	s.add("redact_value", "Describe a secret (bearer token, JWT, bcrypt hash, API key) as {len, sha256_prefix, kind} plus a ready-to-paste redacted form. The raw value is hashed in memory and never stored or logged; write the redacted form, not the value, into findings.", obj(map[string]any{"value": p("string", "the secret to describe")}, "value"), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, "/api/redact", map[string]any{"value": argStr(a, "value")})
	})
	s.add("evaluate_finding_cvss", "Evaluate a CVSS v4.0 vector without modifying a finding. Returns canonical vector, score, original rating and finding severity.", obj(map[string]any{"vector": pt("string")}, "vector"), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, "/api/finding-cvss", map[string]any{"vector": argStr(a, "vector")})
	})
}
