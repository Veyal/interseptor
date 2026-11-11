package control

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
)

// The Activity feed is copied into report appendices. The MCP server masks what
// it reports, but POST /api/activity also accepts rows from an out-of-process
// (stdio) reporter, which may be an older binary that does not. Masking at the
// one place every row is stored covers both.
func TestActivityRowsAreMaskedWhereTheyAreStored(t *testing.T) {
	h, st, _ := newHub(t)
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXZhbHVl"
	if err := h.recordMCPActivityChecked(mcp.Activity{
		Tool:    "send_request",
		Summary: "headers=Authorization: Bearer " + jwt,
		Result:  `{"token":"` + jwt + `"}`,
		Intent:  "replay with password=hunter2-correct-horse",
		OK:      true,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListActivity(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %d err %v", len(rows), err)
	}
	got := rows[0]
	for name, v := range map[string]string{"summary": got.Summary, "result": got.Result, "intent": got.Intent} {
		if strings.Contains(v, jwt) || strings.Contains(v, "hunter2-correct-horse") {
			t.Errorf("%s still carries a credential: %q", name, v)
		}
	}
	if got.Tool != "send_request" || !got.OK {
		t.Fatalf("non-text fields changed: %+v", got)
	}
	if !strings.Contains(got.Summary, "headers=") || !strings.Contains(got.Intent, "replay with") {
		t.Fatalf("masking removed ordinary text: %+v", got)
	}

	// Ordinary text is stored as written.
	plain := store.Activity{Tool: "list_flows", Summary: "host=example.com limit=50", Result: "12 flows", Intent: "triage"}
	stored, err := (&metaAPI{h}).recordActivity(plain)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Summary != plain.Summary || stored.Result != plain.Result || stored.Intent != plain.Intent {
		t.Fatalf("plain row changed: %+v", stored)
	}
}
