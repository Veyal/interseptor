package control

import "testing"

// End-to-end: the captured-flow (history) access-control matrix runs through the
// one collmatrix engine over the real sender. An anonymous replay that still
// succeeds on a captured flow is flagged as a candidate.
func TestCollmatrixFlowRunReplaysCapturedFlow(t *testing.T) {
	f := newCollFixture(t)

	// Two saved identities (the same store the authz modal uses).
	f.must("POST", "/api/authz", map[string]any{"identities": []map[string]any{
		{"name": "admin", "headers": "Authorization: Bearer admin"},
		{"name": "user", "headers": "Authorization: Bearer user"},
	}}, asUI, 200, nil)

	// Capture a flow by sending one from Repeater to the loopback target.
	var sent struct {
		ID int64 `json:"id"`
	}
	f.must("POST", "/api/repeater/send", map[string]any{"url": f.target.URL + "/admin/report"}, asUI, 200, &sent)
	if sent.ID == 0 {
		t.Fatal("no captured flow id")
	}

	var out struct {
		Matrix struct {
			Identities []string `json:"identities"`
			Baseline   string   `json:"baseline"`
			Rows       []struct {
				Cells []struct {
					Class string `json:"class"`
					Flag  string `json:"flag"`
				} `json:"cells"`
			} `json:"rows"`
			Summary struct {
				Violations int `json:"violations"`
			} `json:"summary"`
		} `json:"matrix"`
	}
	f.must("POST", "/api/collmatrix/flow-run", map[string]any{"flowIds": []int64{sent.ID}}, asUI, 200, &out)

	m := out.Matrix
	if len(m.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(m.Rows))
	}
	anon := -1
	for i, n := range m.Identities {
		if n == "anonymous" {
			anon = i
		}
	}
	if anon < 0 {
		t.Fatalf("anonymous not a column: %v", m.Identities)
	}
	// The loopback target answers 200 for everyone, so the no-credential replay
	// succeeding is a candidate (expected-deny got success) — flagged, not confirmed.
	c := m.Rows[0].Cells[anon]
	if c.Class != "success" || c.Flag != "violation" {
		t.Fatalf("anonymous cell = %+v, want success+violation", c)
	}
	if m.Summary.Violations < 1 {
		t.Fatalf("summary violations = %d, want >=1", m.Summary.Violations)
	}
}
