package mcp

import (
	"fmt"
	"net/http"
	"net/url"
)

func (s *Server) registerFindingReviewTools() {
	s.add("finding_readiness", "Check selected finding statuses for final report readiness. Returns the same actionable field/capability checks shown by the UI; these assess completeness, not independent exploit verification.", obj(map[string]any{"statuses": pt("string"), "tag": pt("string")}), func(a map[string]any) (string, error) {
		q := url.Values{}
		q.Set("statuses", argStr(a, "statuses"))
		q.Set("tag", argStr(a, "tag"))
		return s.apiGet("/api/findings/readiness?" + q.Encode())
	})
	s.add("list_finding_revisions", "List immutable finding revision metadata, including deleted findings. Use before for older pages. No raw evidence in the list.", obj(map[string]any{"id": pt("integer"), "before": pt("integer")}, "id"), func(a map[string]any) (string, error) {
		return s.apiGet(fmt.Sprintf("/api/finding-revisions/%d?before=%d", argInt(a, "id", 0), argInt(a, "before", 0)))
	})
	s.add("get_finding_revision", "Read one historical finding snapshot and its field-level diff. May contain sensitive recorded evidence; handle like the current finding.", obj(map[string]any{"id": pt("integer"), "revisionId": pt("integer")}, "id", "revisionId"), func(a map[string]any) (string, error) {
		return s.apiGet(fmt.Sprintf("/api/finding-revisions/%d/%d", argInt(a, "id", 0), argInt(a, "revisionId", 0)))
	})
	s.add("restore_finding_revision", "Restore a selected historical finding version, including a deleted finding. Appends a new revision; separately purged traffic remains missing. Obtain the operator's intent before replacing current report content.", obj(map[string]any{"id": pt("integer"), "revisionId": pt("integer"), "reason": pt("string")}, "id", "revisionId"), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, fmt.Sprintf("/api/finding-revisions/%d/%d/restore", argInt(a, "id", 0), argInt(a, "revisionId", 0)), map[string]any{"reason": argStr(a, "reason")})
	})
	s.add("preview_finding_targets", "Preview exact target deduplication and optional path templates without saving. Evidence links are retained. Apply templates only after reviewer approval through update_finding.", obj(map[string]any{"targets": findingTargetsSchema(), "legacy": pt("string")}), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, "/api/finding-targets/preview", a)
	})
	s.add("evaluate_finding_cvss", "Evaluate a CVSS v4.0 vector without modifying a finding. Returns canonical vector, score, original rating and finding severity.", obj(map[string]any{"vector": pt("string")}, "vector"), func(a map[string]any) (string, error) {
		return s.api(http.MethodPost, "/api/finding-cvss", map[string]any{"vector": argStr(a, "vector")})
	})
}
