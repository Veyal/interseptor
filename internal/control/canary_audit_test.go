package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
)

// TestCanaryAuditAIChannelReads is the cross-surface half of the secret audit
// (archive, vault, bundle and merge have their own canary tests): the same
// secrets seeded for the archive audit must not appear in anything the AI/MCP
// channel can read, and nor in the project bundle. Sending is deliberately not
// exercised here: a sent request is captured History evidence and keeps its
// wire bytes (see docs/collections.md, Secrets), and the test must not dial.
func TestCanaryAuditAIChannelReads(t *testing.T) {
	f := newCollFixture(t)
	seedArchiveSecrets(t, f.st)
	cs, err := f.st.ListCollections()
	if err != nil || len(cs) != 1 {
		t.Fatalf("collections: %v %d", err, len(cs))
	}
	envs, err := f.st.ListEnvironments()
	if err != nil || len(envs) == 0 {
		t.Fatalf("environments: %v %d", err, len(envs))
	}
	reads := []string{
		"/api/collections",
		"/api/collections/" + cs[0].UID,
		"/api/collections/" + cs[0].UID + "/scripts",
		"/api/collections/" + cs[0].UID + "/runs",
		"/api/environments",
		"/api/environments/" + envs[0].UID,
		"/api/variables/environment/" + envs[0].UID,
		"/api/variables/environment/" + envs[0].UID + "?reveal=1",
	}
	for _, p := range reads {
		_, body := f.do(http.MethodGet, p, nil, asAI)
		if strings.Contains(body, archiveCanary) {
			t.Errorf("AI-source GET %s leaked the canary", p)
		}
	}
	srv := mcp.New(f.ts.URL)
	for tool, args := range map[string]map[string]any{
		"list_collections":       {},
		"get_collection":         {"collectionUid": cs[0].UID},
		"script_approval_status": {"collectionUid": cs[0].UID},
	} {
		out, _ := srv.Call(tool, args)
		if strings.Contains(out, archiveCanary) {
			t.Errorf("MCP %s leaked the canary", tool)
		}
	}
	rec := httptest.NewRecorder()
	(&projectAPI{Hub: f.h}).exportProject(rec, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if strings.Contains(rec.Body.String(), archiveCanary) {
		i := strings.Index(rec.Body.String(), archiveCanary)
		t.Errorf("project bundle leaked the canary: ...%s...", rec.Body.String()[max(0, i-300):i+80])
	}
}
