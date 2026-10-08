package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/version"
)

const repoRoot = "../.."

// copyDocs copies the files the agent generators read into a temporary root.
func copyDocs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := append([]string{"docs/cli-reference.md", llmsFile, llmsFullFile}, llmsFullSources...)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAgentFilesAreCurrent(t *testing.T) {
	if err := checkAgentFiles(repoRoot); err != nil {
		t.Fatalf("regenerate with go run ./tools/docscheck generate: %v", err)
	}
}

// Adding or removing a tool or route without regenerating must fail the check.
func TestAgentReferenceDriftIsDetected(t *testing.T) {
	for _, tc := range []struct{ name, row string }{
		{"tool removed", "| `get_flow` |"},
		{"route removed", "| GET | `/api/version` |"},
	} {
		root := copyDocs(t)
		file := filepath.Join(root, agentSource)
		data, _ := os.ReadFile(file)
		doc := string(data)
		if !strings.Contains(doc, tc.row) {
			t.Fatalf("%s: reference lacks %q", tc.name, tc.row)
		}
		var kept []string
		for _, line := range strings.Split(doc, "\n") {
			if !strings.HasPrefix(line, tc.row) {
				kept = append(kept, line)
			}
		}
		if err := os.WriteFile(file, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		err := checkAgentFiles(root)
		if err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("%s: checkAgentFiles = %v, want a stale-reference error", tc.name, err)
		}
		if err := writeAgentFiles(root); err != nil {
			t.Fatal(err)
		}
		if err := checkAgentFiles(root); err != nil {
			t.Fatalf("%s: after regenerate: %v", tc.name, err)
		}
	}
}

func TestAgentReferenceListsEveryToolAndRoute(t *testing.T) {
	ref := agentReference()
	names := mcp.New("http://127.0.0.1:1").ToolNames()
	for _, name := range names {
		if !strings.Contains(ref, "| `"+name+"` |") {
			t.Errorf("reference lacks tool %s", name)
		}
	}
	if !strings.Contains(ref, "### MCP tools ("+itoa(len(names))+")") {
		t.Errorf("reference tool count heading does not say %d", len(names))
	}
	routes := control.RouteCatalog()
	for _, r := range routes {
		if !strings.Contains(ref, "| "+r.Method+" | `"+r.Path+"` |") {
			t.Errorf("reference lacks route %s %s", r.Method, r.Path)
		}
	}
	if !strings.Contains(ref, "### REST routes ("+itoa(len(routes))+")") {
		t.Errorf("reference route count heading does not say %d", len(routes))
	}
	for _, field := range []string{"proofReview.execution", "blocks[].role", "targets[].flow_ids"} {
		if !strings.Contains(ref, "| `"+field+"` |") {
			t.Errorf("reference lacks finding field %s", field)
		}
	}
	if err := validateLiquidSafe(ref); err != nil {
		t.Errorf("reference is not Liquid-safe: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAgentGuideNamesExistInCode(t *testing.T) {
	body, err := agentGuide(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAgentGuide(repoRoot, body); err != nil {
		t.Fatal(err)
	}
}

func TestAgentGuideRejectsInventedNames(t *testing.T) {
	base, err := agentGuide(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	marker := "## Troubleshooting and FAQ"
	if !strings.Contains(base, marker) {
		t.Fatalf("guide lost %q", marker)
	}
	for name, injected := range map[string]string{
		"tool":        "Call `delete_everything` now.\n",
		"field":       "Set `proofReviewLevel` first.\n",
		"route":       "Use `POST /api/nonexistent/route`.\n",
		"code route":  "```bash\ncurl $C/api/not-a-route\n```\n",
		"flag":        "Run `interseptor --turbo`.\n",
		"json tool":   "```json\n{\"name\":\"made_up_tool\",\"arguments\":{}}\n```\n",
		"json field":  "```json\n{\"name\":\"get_flow\",\"arguments\":{\"id\":1,\"bogus\":true}}\n```\n",
		"json nested": "```json\n{\"name\":\"create_finding\",\"arguments\":{\"title\":\"t\",\"proofReview\":{\"verdict\":\"x\"}}}\n```\n",
		"missing req": "```json\n{\"name\":\"get_flow\",\"arguments\":{}}\n```\n",
	} {
		doc := strings.Replace(base, marker, injected+"\n"+marker, 1)
		if err := validateAgentGuide(repoRoot, doc); err == nil {
			t.Errorf("%s: invented name was accepted", name)
		}
	}
}

func TestGuideExtraNamesAppearInTheirSource(t *testing.T) {
	for name, source := range guideExtraNames {
		if source == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, source))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), name) {
			t.Errorf("%s is allowlisted from %s but does not appear there", name, source)
		}
	}
}

// Documented enumerations must still exist in the tool schemas and descriptions.
func TestGuideEnumValuesExistInCode(t *testing.T) {
	_, corpus := knownGuideNames()
	for _, value := range []string{
		"context", "setup", "baseline", "action", "result", "control", "retest", "observation",
		"demonstrated", "prerequisite_only", "not_executed",
		"confirmed", "partially_confirmed", "not_reproduced", "refuted",
		"enables", "enabled_by", "chain", "duplicate", "escalates",
		"tentative", "firm", "certain",
		"production", "staging", "development", "testing", "local",
		"needs_verification", "false_positive", "wont_fix",
		"captured_flow", "browser_screenshot", "device_screenshot", "flow_preview", "evidence_render", "generated_image", "operator_upload", "tool_output",
		"timeline", "distribution", "race", "strip", "authz_matrix", "flow_diff", "flow_waterfall", "finding_chain",
	} {
		if !strings.Contains(corpus, value) {
			t.Errorf("documented value %q is not in any tool schema or route description", value)
		}
	}
}

func TestLLMSDescribesEveryPublicPage(t *testing.T) {
	slugs := map[string]bool{}
	reserved := []string{"AGENTS", "CLAUDE", "README", "SECURITY", "CONTRIBUTING", "CHANGELOG", "DESIGN", "LICENSE", "Makefile"}
	for _, meta := range publicDocs {
		slugs[meta.Slug] = true
		if llmsDescriptions[meta.Slug] == "" {
			t.Errorf("llmsDescriptions lacks %s", meta.Slug)
		}
		// Generated root pages share a directory with repository files; macOS
		// and Windows file systems are case-insensitive.
		for _, r := range reserved {
			if strings.EqualFold(meta.Slug, r) {
				t.Errorf("slug %q collides with %s on case-insensitive file systems", meta.Slug, r)
			}
		}
	}
	for slug := range llmsDescriptions {
		if !slugs[slug] {
			t.Errorf("llmsDescriptions describes unknown page %s", slug)
		}
	}
}

func TestLLMSTextFollowsLlmstxtFormat(t *testing.T) {
	text := llmsText()
	lines := strings.Split(text, "\n")
	if lines[0] != "# Interseptor" || !strings.HasPrefix(lines[2], "> ") {
		t.Fatalf("llms.txt must start with an H1 and a blockquote summary:\n%s", strings.Join(lines[:4], "\n"))
	}
	if strings.Count(text, "\n# ") != 0 {
		t.Error("llms.txt must have a single H1")
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") && !strings.Contains(line, "](https://veyal.github.io/interseptor/") {
			t.Errorf("llms.txt list item is not an absolute site link: %s", line)
		}
	}
	for _, want := range []string{"## Agent guide", "## Core guides", "## Optional", version.LLMSFullURL, version.DocsSite + "/ai-agents/"} {
		if !strings.Contains(text, want) {
			t.Errorf("llms.txt lacks %q", want)
		}
	}
	for _, meta := range publicDocs {
		if !strings.Contains(text, version.DocsSite+"/"+meta.Slug+"/") {
			t.Errorf("llms.txt does not list %s", meta.Slug)
		}
	}
}

func TestLLMSFullIsSelfContainedPlainText(t *testing.T) {
	full, err := llmsFullText(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "# ") {
		t.Error("llms-full.txt must start with a Markdown H1")
	}
	for _, bad := range []string{"{% raw", "{% endraw", "relative_url", "layout: default", "](../", "](docs/", "](product/"} {
		if strings.Contains(full, bad) {
			t.Errorf("llms-full.txt contains %q", bad)
		}
	}
	for _, want := range []string{"## Hard rules", "### Rejections and how to fix them", "### MCP tools (", "### REST routes (", "Recipe 4", "## Canonical finding envelope", "## Connect an external agent with MCP", "](" + version.DocsSite + "/collections/)"} {
		if !strings.Contains(full, want) {
			t.Errorf("llms-full.txt lacks %q", want)
		}
	}
}

func TestRewriteLinksTextMakesLinksAbsolute(t *testing.T) {
	got := rewriteLinksText("[a](collections.md#x) [b](../CONTRIBUTING.md) [c](#local) [d](https://example.com/) [e](product/mcp-cookbook.md)", "docs/agents.md")
	for _, want := range []string{
		"](" + version.DocsSite + "/collections/#x)",
		"](" + repositoryURL + "/blob/main/CONTRIBUTING.md)",
		"](#local)", "](https://example.com/)",
		"](" + version.DocsSite + "/mcp-cookbook/)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewriteLinksText lacks %q in %s", want, got)
		}
	}
}

func TestValidateLiquidSafe(t *testing.T) {
	if validateLiquidSafe("plain {\"a\":{\"b\":1}} text") != nil {
		t.Error("JSON braces without a double open brace are safe")
	}
	if validateLiquidSafe("a {{ b }} c") == nil || validateLiquidSafe("a {% if %}") == nil {
		t.Error("Liquid delimiters must be rejected")
	}
	if validateLiquidSafe("a {% raw %}{{ b }}{% endraw %} c") != nil {
		t.Error("raw-wrapped Liquid must be accepted")
	}
	if got := liquidSafe("x {{token}} y"); validateLiquidSafe(got) != nil {
		t.Errorf("liquidSafe left %q unprotected", got)
	}
}

func TestDocsSiteMatchesJekyllConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "_config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var url, base string
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "url:"); ok {
			url = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "baseurl:"); ok {
			base = strings.TrimSpace(v)
		}
	}
	if version.DocsSite != url+base {
		t.Fatalf("version.DocsSite = %q, Jekyll serves %q", version.DocsSite, url+base)
	}
}

func TestValidateBuiltLLMS(t *testing.T) {
	root, site := t.TempDir(), t.TempDir()
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{llmsFile, llmsFullFile} {
		write(root, name, "# T\n\nbody\n")
		write(site, name, "# T\n\nbody\n")
	}
	if err := validateBuiltLLMS(site, root); err != nil {
		t.Fatalf("matching raw files rejected: %v", err)
	}
	write(site, llmsFile, "<!DOCTYPE html><html><body># T</body></html>")
	if err := validateBuiltLLMS(site, root); err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("HTML-wrapped llms.txt = %v, want rejection", err)
	}
	write(site, llmsFile, "# T\n\nbody\n")
	write(site, llmsFullFile, "# T\n\nchanged\n")
	if err := validateBuiltLLMS(site, root); err == nil {
		t.Fatal("drifted built llms-full.txt accepted")
	}
	os.Remove(filepath.Join(site, llmsFile))
	if err := validateBuiltLLMS(site, root); err == nil {
		t.Fatal("missing built llms.txt accepted")
	}
}
