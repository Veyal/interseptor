package control

import (
	"strings"
	"testing"
)

// Environments must be creatable from the UI, and a bulk run must show its plan first.
func TestCollectionsEnvUICreateAndRunPlanWiring(t *testing.T) {
	read := func(p string) string {
		b, err := uiFS.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}
	env := read("ui/js/collections-env.js") + read("ui/js/collections-env-model.js")
	for _, want := range []string{"'POST', '/api/environments'", "'DELETE', '/api/environments/", "uiConfirm(", "esc(w.name)", "collectionUid", "collEnvNew", "collEnvManage"} {
		if !strings.Contains(env, want) {
			t.Errorf("collections-env.js missing %q", want)
		}
	}
	html := read("ui/index.html")
	for _, id := range []string{`id="collEnvNew"`, `id="collEnvManage"`} {
		if !strings.Contains(html, id) {
			t.Errorf("index.html missing %s", id)
		}
	}
	sheets := read("ui/js/collections-sheets.js")
	for _, want := range []string{"buildRunPlan(", "planConfirmed(", "planSummary(", "CONFIRM_PHRASE", "GraphQL", "WSDL", "REST Client"} {
		if !strings.Contains(sheets, want) {
			t.Errorf("collections-sheets.js missing %q", want)
		}
	}
	// runCollection must reach execRun only through the plan sheet.
	i := strings.Index(sheets, "export async function runCollection")
	j := strings.Index(sheets, "function paintRunPlan")
	if i < 0 || j < i {
		t.Fatal("runCollection / paintRunPlan not found")
	}
	body := sheets[i:j]
	if !strings.Contains(body, "paintRunPlan(") {
		t.Error("runCollection must route through paintRunPlan")
	}
	if strings.Contains(sheets[:i], "execRun(") {
		t.Error("execRun must not be called before runCollection builds a plan")
	}
}
