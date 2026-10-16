package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// fullToolNames is the frozen default MCP surface. The profile work must not
// add, drop or rename a tool in the default (`full`) profile, so this list is
// compared against the live registry. Update it only when the default surface
// is deliberately changed, and say so in CHANGELOG.md.
var fullToolNames = []string{
	"finding_readiness", "list_finding_revisions", "get_finding_revision", "restore_finding_revision",
	"preview_finding_targets", "normalize_finding_targets", "redact_value", "evaluate_finding_cvss",
	"render_intruder_preview", "render_evidence",
	"list_collections", "get_collection", "run_request", "run_collection", "set_variable",
	"script_approval_status", "collection_identity_matrix", "collection_openapi_coverage",
	"collection_intruder_handoff", "collection_example_diff", "collection_run_timing",
	"collection_attach_run_evidence",
	"list_flows", "get_flow", "analyze_flow", "audit_endpoint", "flow_as_curl", "diff_flows",
	"set_note", "tag_flow", "untag_flow", "list_tags",
	"get_interception_setup", "set_interception_setup", "annotate_flow_interception",
	"get_engagement_brief", "set_engagement_brief", "get_notes", "set_notes", "append_notes",
	"create_finding", "list_findings", "get_finding", "list_finding_tags", "update_finding",
	"add_finding_poc", "record_finding_from_flow", "add_finding_image", "classify_finding_image",
	"render_flow_preview", "remove_finding_poc", "delete_finding",
	"export_report", "export_full_project", "import_full_project",
	"vault_list", "vault_backup", "vault_import", "vault_merge",
	"send_request", "start_intruder", "intruder_state",
	"run_scanner", "list_issues", "scan_report",
	"list_checks", "test_check", "save_check", "delete_check",
	"list_codecs", "test_codec", "save_codec", "delete_codec", "get_flow_decoded", "encode_codec",
	"list_packs", "pack_info",
	"get_intercept", "set_intercept", "set_response_intercept",
	"forward_request", "drop_request", "forward_response", "drop_response",
	"list_rules", "add_rule", "update_rule", "delete_rule",
	"list_ws_frames", "set_ws_frame_note", "ws_send",
	"list_scope", "add_scope_rule", "get_settings", "set_session", "decode", "ca_info",
	"android_status", "android_setup", "android_teardown",
	"ios_status", "ios_setup", "ios_install_ca", "ios_ssh_status", "ios_ssh_setup", "ios_ssh_install_ca",
	"check_readiness", "detect_ssl_pinning", "scope_from_url",
	"request_human_input", "get_human_response", "host_stats", "prune_history",
	"run_login_macro", "get_authz", "list_authz", "set_authz", "add_authz_identity",
	"remove_authz_identity", "authz_differential", "auth_timeline", "authz_run",
	"authz_check_sessions", "cross_host_token_replay",
	"oob_state", "oob_new", "oob_set_base", "oob_enable",
	"get_flow_auth", "promote_flow_to_authz", "set_login_macro_from_flow", "set_login_macro",
	"test_login_macro",
}

// The default profile must expose exactly the tools it exposed before profiles
// existed — same names, same order, nothing hidden by accident.
func TestFullProfileIsUnchanged(t *testing.T) {
	got := New("http://127.0.0.1:1").ToolNames()
	if len(got) != len(fullToolNames) {
		t.Fatalf("full profile has %d tools, frozen list has %d", len(got), len(fullToolNames))
	}
	for i := range got {
		if got[i] != fullToolNames[i] {
			t.Fatalf("tool %d = %q, want %q", i, got[i], fullToolNames[i])
		}
	}
	if p := New("http://127.0.0.1:1").Profile(); p != ProfileFull {
		t.Fatalf("default profile = %q, want %q", p, ProfileFull)
	}
}

func TestParseProfileAcceptsNamesAliasesAndRejectsJunk(t *testing.T) {
	ok := map[string]Profile{
		"":            ProfileFull,
		"full":        ProfileFull,
		"FULL":        ProfileFull,
		"  full  ":    ProfileFull,
		"web":         ProfileWebPentest,
		"web-pentest": ProfileWebPentest,
		"web_pentest": ProfileWebPentest,
		"webpentest":  ProfileWebPentest,
		"reporting":   ProfileReporting,
		"report":      ProfileReporting,
		"mobile":      ProfileMobile,
		"MoBiLe":      ProfileMobile,
	}
	for in, want := range ok {
		got, err := ParseProfile(in)
		if err != nil {
			t.Errorf("ParseProfile(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseProfile(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"nope", "api", "web pentest", "full,web"} {
		if _, err := ParseProfile(bad); err == nil {
			t.Errorf("ParseProfile(%q) accepted an unknown profile", bad)
		}
	}
}

// Profile membership must be deterministic and declared, not derived from
// name prefixes, and every declared name must be a tool that really exists.
func TestProfileToolsAreDeclaredRegisteredAndDeterministic(t *testing.T) {
	registered := map[string]bool{}
	for _, n := range New("http://127.0.0.1:1").ToolNames() {
		registered[n] = true
	}
	for _, p := range Profiles() {
		names := ProfileTools(p)
		if len(names) == 0 {
			t.Fatalf("profile %q exposes no tools", p)
		}
		seen := map[string]bool{}
		for _, n := range names {
			if !registered[n] {
				t.Errorf("profile %q lists %q, which is not a registered tool", p, n)
			}
			if seen[n] {
				t.Errorf("profile %q lists %q twice", p, n)
			}
			seen[n] = true
		}
		if again := ProfileTools(p); !slices.Equal(names, again) {
			t.Errorf("profile %q tool order is not deterministic", p)
		}
	}
	if n := len(ProfileTools(ProfileFull)); n != len(fullToolNames) {
		t.Errorf("full profile tools = %d, want %d", n, len(fullToolNames))
	}
}

// Each curated profile exposes exactly the tools documented in
// docs/api-and-mcp.md. Keep the two in step.
func TestCuratedProfilesExposeTheirDocumentedTools(t *testing.T) {
	want := map[Profile][]string{
		ProfileWebPentest: {
			"add_authz_identity", "add_finding_image", "add_finding_poc", "analyze_flow",
			"authz_differential", "authz_run", "ca_info", "check_readiness", "create_finding",
			"cross_host_token_replay", "get_flow", "get_human_response", "host_stats",
			"intruder_state", "list_authz", "list_findings", "list_flows", "list_issues", "list_scope",
			"oob_new", "oob_state", "promote_flow_to_authz", "render_flow_preview",
			"request_human_input", "run_login_macro", "run_scanner", "scope_from_url",
			"send_request", "set_login_macro_from_flow", "start_intruder", "update_finding",
		},
		ProfileReporting: {
			"check_readiness", "evaluate_finding_cvss", "export_full_project", "export_report",
			"finding_readiness", "get_finding", "get_human_response", "list_findings",
			"normalize_finding_targets", "preview_finding_targets", "redact_value",
			"render_evidence", "render_flow_preview", "request_human_input", "update_finding",
		},
		ProfileMobile: {
			"android_setup", "android_status", "android_teardown", "ca_info", "check_readiness",
			"detect_ssl_pinning", "get_human_response", "ios_install_ca", "ios_setup",
			"ios_ssh_install_ca", "ios_ssh_setup", "ios_ssh_status", "ios_status", "list_flows",
			"request_human_input", "scope_from_url",
		},
	}
	for p, wantNames := range want {
		got := append([]string(nil), ProfileTools(p)...)
		slices.Sort(got)
		if !slices.Equal(got, wantNames) {
			t.Errorf("profile %q tools =\n  %v\nwant\n  %v", p, got, wantNames)
		}
	}
}

// Live intercept holds a real browser socket open for the length of the agent's
// round trip, so the packet-holding tools stay out of every curated profile.
func TestCuratedProfilesExcludeLiveInterceptHolding(t *testing.T) {
	for _, p := range []Profile{ProfileWebPentest, ProfileReporting, ProfileMobile} {
		for _, name := range liveInterceptTools {
			if slices.Contains(ProfileTools(p), name) {
				t.Errorf("profile %q exposes packet-holding tool %q", p, name)
			}
		}
	}
	// They stay in `full`, with their deprecation stated in the description.
	s := New("http://127.0.0.1:1")
	for _, name := range liveInterceptTools {
		desc, _, ok := s.ToolMeta(name)
		if !ok {
			t.Fatalf("%s was deleted; it must stay available in the full profile", name)
		}
		if !strings.Contains(desc, "DEPRECATED over MCP") {
			t.Errorf("%s description does not state the MCP deprecation: %q", name, desc)
		}
		if !strings.Contains(desc, "send_request") {
			t.Errorf("%s description does not point at the replay alternative", name)
		}
	}
}

// tools/list and the schema hash must both honour the profile, so a client that
// switches profiles is told to reload its tool cache.
func TestToolListAndSchemaHashHonourProfile(t *testing.T) {
	full := New("http://127.0.0.1:1")
	web := NewProfile("http://127.0.0.1:1", ProfileWebPentest)

	if got := len(web.toolList()); got != len(ProfileTools(ProfileWebPentest)) {
		t.Fatalf("web tools/list = %d entries, want %d", got, len(ProfileTools(ProfileWebPentest)))
	}
	if len(web.ToolNames()) != len(ProfileTools(ProfileWebPentest)) {
		t.Fatalf("web ToolNames = %d, want %d", len(web.ToolNames()), len(ProfileTools(ProfileWebPentest)))
	}
	for _, entry := range web.toolList() {
		if !slices.Contains(ProfileTools(ProfileWebPentest), entry["name"].(string)) {
			t.Errorf("web tools/list leaked %v", entry["name"])
		}
	}
	if full.SchemaHash() == web.SchemaHash() {
		t.Error("schema hash is identical across profiles; a profile switch would go unnoticed")
	}
	// ToolMeta stays whole-registry so docs generation and diagnostics still see
	// every tool regardless of the serving profile.
	if _, _, ok := web.ToolMeta("drop_request"); !ok {
		t.Error("ToolMeta must report the whole registry, not just the active profile")
	}
}

// A tool outside the active profile is not callable: a profile that advertises
// 30 tools but silently runs 133 would make tools/list a lie.
func TestOutOfProfileCallIsRefusedWithAWayOut(t *testing.T) {
	web := NewProfile("http://127.0.0.1:1", ProfileWebPentest)
	_, err := web.Call("drop_request", map[string]any{"id": 1})
	if err == nil {
		t.Fatal("calling an out-of-profile tool succeeded")
	}
	for _, want := range []string{"drop_request", string(ProfileWebPentest), "full"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err.Error(), want)
		}
	}
	// Unknown tools keep their own error, which must not blame the profile.
	if _, err := web.Call("no_such_tool", nil); err == nil || strings.Contains(err.Error(), "profile") {
		t.Errorf("unknown-tool error = %v, want the plain unknown-tool message", err)
	}
	// The default profile reaches the tool: it gets as far as the (unreachable)
	// control API instead of being refused by the profile gate.
	if _, err := New("http://127.0.0.1:1").Call("drop_request", map[string]any{"id": 1}); err != nil &&
		strings.Contains(err.Error(), "MCP profile") {
		t.Errorf("full profile refused drop_request: %v", err)
	}
}

// Reduced profiles must not repeat the long finding-writing contract in every
// finding tool description: it is already carried once by initialize.
func TestReducedProfilesCondenseTheDuplicatedFindingGuide(t *testing.T) {
	full := New("http://127.0.0.1:1")
	web := NewProfile("http://127.0.0.1:1", ProfileWebPentest)

	for _, name := range []string{"create_finding", "update_finding"} {
		var fullDesc, webDesc string
		for _, e := range full.toolList() {
			if e["name"] == name {
				fullDesc = e["description"].(string)
			}
		}
		for _, e := range web.toolList() {
			if e["name"] == name {
				webDesc = e["description"].(string)
			}
		}
		if !strings.Contains(fullDesc, findingFormatGuide) {
			t.Fatalf("full profile %s no longer carries the finding format guide", name)
		}
		if webDesc == "" {
			t.Fatalf("%s missing from the web profile tools/list", name)
		}
		if strings.Contains(webDesc, findingFormatGuide) {
			t.Errorf("web profile %s still repeats the full finding format guide", name)
		}
		if len(webDesc) >= len(fullDesc) {
			t.Errorf("web profile %s description (%d) is not shorter than full (%d)", name, len(webDesc), len(fullDesc))
		}
		for _, want := range []string{"claim", "risk", "target", "proof"} {
			if !strings.Contains(strings.ToLower(webDesc), want) {
				t.Errorf("condensed %s description drops the %q field", name, want)
			}
		}
	}
	// The contract itself must still reach the agent through initialize.
	if !strings.Contains(instructionsFor(ProfileWebPentest, web.visibleSet()), findingFormatGuide) {
		t.Error("web profile instructions drop the finding format guide")
	}
}

// Connect instructions must not advertise tools the active profile does not
// serve, and the full profile's instructions must stay as they were.
func TestInstructionsStayHonestPerProfile(t *testing.T) {
	if got := instructionsFor(ProfileFull, New("http://127.0.0.1:1").visibleSet()); got != mcpInstructions() {
		t.Fatal("full-profile instructions changed")
	}
	for _, section := range instructionSections() {
		if !strings.Contains(mcpInstructions(), section) {
			t.Fatalf("full instructions do not contain section %q…", firstLine(section, 40))
		}
	}
	known := map[string]bool{}
	for _, n := range New("http://127.0.0.1:1").ToolNames() {
		known[n] = true
	}
	guide := formatGuideSection()
	for _, p := range []Profile{ProfileWebPentest, ProfileReporting, ProfileMobile} {
		s := NewProfile("http://127.0.0.1:1", p)
		visible := s.visibleSet()
		instr := instructionsFor(p, visible)
		// Every workflow section the profile keeps must be runnable with the
		// tools it serves. The finding-format guide is prose about how to write
		// a finding, not a workflow, so it is gated on the finding tools.
		for _, sec := range includedSections(visible, known) {
			if sec == guide {
				if !visible["create_finding"] && !visible["update_finding"] {
					t.Errorf("profile %q keeps the finding format guide without a finding tool", p)
				}
				continue
			}
			for _, name := range toolNamesIn(sec, known) {
				if !visible[name] {
					t.Errorf("profile %q keeps a section naming %q, which it does not serve", p, name)
				}
			}
		}
		if !strings.Contains(instr, "PROFILE: ") || !strings.Contains(instr, string(p)) {
			t.Errorf("profile %q instructions do not state the active profile", p)
		}
	}
	// The web profile must keep the whole documented workflow: it serves every
	// tool those sections name.
	webVisible := NewProfile("http://127.0.0.1:1", ProfileWebPentest).visibleSet()
	webInstr := instructionsFor(ProfileWebPentest, webVisible)
	for _, want := range []string{"SETUP:", "AUTH:", "RECON:", "SCAN:", "RECORD:", "ASK FOR FINDINGS:", "HUMAN INPUT"} {
		if !strings.Contains(webInstr, want) {
			t.Errorf("web profile instructions dropped the %q section", want)
		}
	}
	// The mobile profile has no scanner or findings, so those sections go.
	mobileVisible := NewProfile("http://127.0.0.1:1", ProfileMobile).visibleSet()
	mobileInstr := instructionsFor(ProfileMobile, mobileVisible)
	for _, unwanted := range []string{"SCAN:", "RECORD:", "AUTH:"} {
		if strings.Contains(mobileInstr, unwanted) {
			t.Errorf("mobile profile instructions keep the %q section", unwanted)
		}
	}
	if !strings.Contains(mobileInstr, "SETUP:") {
		t.Error("mobile profile instructions dropped SETUP")
	}
}

// The profile a server serves must be visible over the wire so a client can
// tell which surface it is holding.
func TestInitializeAndCapabilitiesReportTheProfile(t *testing.T) {
	web := NewProfile("http://127.0.0.1:1", ProfileWebPentest)
	res, rpcErr := web.dispatch("initialize", json.RawMessage(`{"protocolVersion":"2024-11-05"}`))
	if rpcErr != nil {
		t.Fatalf("initialize: %+v", rpcErr)
	}
	m := res.(map[string]any)
	if m["profile"] != string(ProfileWebPentest) {
		t.Errorf("initialize profile = %v, want %v", m["profile"], ProfileWebPentest)
	}
	caps := web.Capabilities()
	if caps["profile"] != string(ProfileWebPentest) {
		t.Errorf("capabilities profile = %v, want %v", caps["profile"], ProfileWebPentest)
	}
	tools, ok := caps["tools"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.tools missing: %#v", caps["tools"])
	}
	if tools["count"] != len(ProfileTools(ProfileWebPentest)) {
		t.Errorf("capabilities.tools.count = %v, want %d", tools["count"], len(ProfileTools(ProfileWebPentest)))
	}
	names, ok := caps["profiles"].([]string)
	if !ok || len(names) != len(Profiles()) {
		t.Errorf("capabilities.profiles = %#v, want %d names", caps["profiles"], len(Profiles()))
	}
}

// Measured schema sizes back the documented token numbers, and guard against a
// future edit quietly inflating a curated profile back toward the full surface.
func TestProfileSchemaSizeStaysUnderBudget(t *testing.T) {
	budget := map[Profile]int{
		ProfileWebPentest: 40 << 10,
		ProfileReporting:  22 << 10,
		ProfileMobile:     10 << 10,
	}
	fullSize := toolListBytes(t, New("http://127.0.0.1:1"))
	for p, max := range budget {
		got := toolListBytes(t, NewProfile("http://127.0.0.1:1", p))
		if got > max {
			t.Errorf("profile %q tools/list is %d bytes, over the %d-byte budget", p, got, max)
		}
		if got >= fullSize {
			t.Errorf("profile %q (%d bytes) is not smaller than full (%d bytes)", p, got, fullSize)
		}
	}
	t.Logf("tools/list bytes: full=%d web-pentest=%d reporting=%d mobile=%d",
		fullSize,
		toolListBytes(t, NewProfile("http://127.0.0.1:1", ProfileWebPentest)),
		toolListBytes(t, NewProfile("http://127.0.0.1:1", ProfileReporting)),
		toolListBytes(t, NewProfile("http://127.0.0.1:1", ProfileMobile)))
}

func toolListBytes(t *testing.T, s *Server) int {
	t.Helper()
	b, err := json.Marshal(map[string]any{"tools": s.toolList()})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	return len(b)
}

func TestProfileFromEnvFallsBackToFull(t *testing.T) {
	t.Setenv(ProfileEnvVar, "web")
	if got := ProfileFromEnv(); got != ProfileWebPentest {
		t.Errorf("ProfileFromEnv = %q, want %q", got, ProfileWebPentest)
	}
	t.Setenv(ProfileEnvVar, "nonsense")
	if got := ProfileFromEnv(); got != ProfileFull {
		t.Errorf("ProfileFromEnv with junk = %q, want %q", got, ProfileFull)
	}
	t.Setenv(ProfileEnvVar, "")
	if got := ProfileFromEnv(); got != ProfileFull {
		t.Errorf("ProfileFromEnv unset = %q, want %q", got, ProfileFull)
	}
}

func TestToolNamesInUsesWholeIdentifiers(t *testing.T) {
	known := map[string]bool{"set_login_macro": true, "set_login_macro_from_flow": true, "get_finding": true}
	got := toolNamesIn("call set_login_macro_from_flow then get_finding.", known)
	slices.Sort(got)
	if !slices.Equal(got, []string{"get_finding", "set_login_macro_from_flow"}) {
		t.Fatalf("toolNamesIn = %v; prefixes must not match", got)
	}
}
