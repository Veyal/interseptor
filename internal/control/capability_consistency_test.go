package control

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestParityRESTRoutesAreRegisteredAndIndexed(t *testing.T) {
	wants := []string{
		"POST /api/intercept/response/{id}/forward",
		"POST /api/intercept/response/{id}/drop",
		"POST /api/rules",
		"PUT /api/rules/{id}",
		"DELETE /api/rules/{id}",
		"POST /api/intruder/start",
		"GET /api/export/har",
		"POST /api/import/har",
	}
	indexed := make(map[string]bool, len(apiRoutes))
	for _, route := range apiRoutes {
		indexed[route.Method+" "+route.Path] = true
	}
	registrations, err := os.ReadFile("routes_register.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(registrations)
	for _, want := range wants {
		if !indexed[want] {
			t.Errorf("route catalog missing %s", want)
		}
		if !strings.Contains(src, `HandleFunc("`+want+`"`) {
			t.Errorf("HTTP mux registration missing %s", want)
		}
	}
}

func TestSettingsHARImportExportJourney(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, index,
		`id="exportHAR"`,
		`href="/api/export/har"`,
		`download`,
		`id="importHARBtn"`,
		`id="importHARFile"`,
		`accept=".har,application/json"`,
		`aria-label="Import HAR file"`,
	)
	requireUIContains(t, settings,
		"importHARBtn",
		"importHARFile",
		"uiConfirm(",
		"'/api/import/har'",
		"method:'POST'",
		"loadFlows()",
		"toast('HAR import:",
	)
	requireUIRegex(t, settings, `(?s)importHARFile.*?uiConfirm\(.*?api\('/api/import/har'.*?loadFlows\(\)`)
}

func TestRaceRepeatTerminologyAndAPIFieldStayConsistent(t *testing.T) {
	index := readUIAsset(t, "index.html")
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	if strings.Contains(index, "Null mode") || strings.Contains(tools, "Null mode") {
		t.Fatal(`user-facing "Null mode" terminology remains`)
	}
	requireUIContains(t, index, `data-t="repeat"`, "Race / repeat")
	requireUIContains(t, tools, "Race / repeat", "body.repeat=")
	if strings.Contains(index, `data-t="race"`) || strings.Contains(tools, "attackType:'race'") {
		t.Fatal("UI renamed the stable repeat API value")
	}
}

func TestProviderAndHostedDocumentationConsistency(t *testing.T) {
	providers := []string{"Anthropic", "OpenRouter", "GLM", "Zhipu", "OpenAI"}
	for _, path := range []string{"README.md", "docs/api-and-mcp.md", "docs/FEATURES.md", "docs/architecture.md"} {
		body := readRepoFile(t, strings.Split(path, "/")...)
		for _, provider := range providers {
			if !strings.Contains(body, provider) {
				t.Errorf("%s does not document %s", path, provider)
			}
		}
	}

	hosted := "https://github.com/Veyal/interseptor/blob/main/docs/engagement-closeout.md"
	for _, asset := range []string{"index.html", "js/findings.js"} {
		body := readUIAsset(t, asset)
		if !strings.Contains(body, hosted) {
			t.Errorf("%s does not use the hosted engagement close-out URL", asset)
		}
		if strings.Contains(body, "<code>docs/engagement-closeout.md</code>") {
			t.Errorf("%s still exposes a repository-relative in-app path", asset)
		}
	}
}

func TestCurrentDocsAvoidManualCapabilityCountsAndHistoricalDocsSaySo(t *testing.T) {
	count := regexp.MustCompile(`(?i)\b\d+\s+(?:MCP\s+)?tools\b`)
	for _, path := range []string{"README.md", "docs/api-and-mcp.md", "docs/product/roadmap.md"} {
		body := readRepoFile(t, strings.Split(path, "/")...)
		if count.MatchString(body) {
			t.Errorf("%s contains a manually maintained current tool count: %q", path, count.FindString(body))
		}
	}
	for _, path := range []string{"docs/UI-REDESIGN-ROADMAP.md", "docs/AUDIT-BACKLOG.md"} {
		body := readRepoFile(t, strings.Split(path, "/")...)
		if !strings.Contains(body, "> **Historical snapshot") {
			t.Errorf("%s is not clearly labeled as historical", path)
		}
	}
}
