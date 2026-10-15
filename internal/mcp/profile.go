package mcp

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/Veyal/interseptor/internal/version"
)

// Profiles are named subsets of the MCP tool surface. The full surface is 133
// tools; an eager-loading MCP client injects every schema into every prompt
// before the user has said anything, so a client that only replays requests and
// files findings pays for the mobile, codec, collection and vault tools too. A
// profile is a declared allow-list: deterministic, documented, and asserted by
// tests against the live registry so a renamed tool cannot silently vanish.
//
// `full` is the default and keeps today's surface exactly.
type Profile string

const (
	// ProfileFull is every registered tool — the default.
	ProfileFull Profile = "full"
	// ProfileWebPentest is the replay-first web workflow: scope, flows,
	// Repeater, Intruder, passive scan, authz, OOB, findings.
	ProfileWebPentest Profile = "web-pentest"
	// ProfileReporting is the report pass over findings that already exist:
	// readiness, target cleanup, CVSS, evidence rendering, export.
	ProfileReporting Profile = "reporting"
	// ProfileMobile is device interception setup: Android, iOS, CA, pinning.
	ProfileMobile Profile = "mobile"
)

// ProfileEnvVar selects the profile for `interseptor mcp` (stdio) and the
// default for the Streamable-HTTP endpoint when no ?profile= is given.
const ProfileEnvVar = "INTERSEPTOR_MCP_PROFILE"

// profileAliases maps what people actually type to a profile. Unknown values
// are an error at the edge, never a silent fallback to a different surface.
var profileAliases = map[string]Profile{
	"":            ProfileFull,
	"full":        ProfileFull,
	"all":         ProfileFull,
	"web":         ProfileWebPentest,
	"webpentest":  ProfileWebPentest,
	"web-pentest": ProfileWebPentest,
	"web_pentest": ProfileWebPentest,
	"reporting":   ProfileReporting,
	"report":      ProfileReporting,
	"mobile":      ProfileMobile,
}

// Profiles returns every profile name, full first.
func Profiles() []Profile {
	return []Profile{ProfileFull, ProfileWebPentest, ProfileReporting, ProfileMobile}
}

// ProfileNames returns every profile name as a string, for JSON payloads.
func ProfileNames() []string {
	out := make([]string, 0, len(Profiles()))
	for _, p := range Profiles() {
		out = append(out, string(p))
	}
	return out
}

// ParseProfile resolves a profile name or alias. An empty string is `full`.
func ParseProfile(s string) (Profile, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if p, ok := profileAliases[key]; ok {
		return p, nil
	}
	return ProfileFull, fmt.Errorf("unknown MCP profile %q: use one of %s", s, strings.Join(ProfileNames(), ", "))
}

// ProfileFromEnv reads INTERSEPTOR_MCP_PROFILE, falling back to `full` when it
// is unset or unrecognised so a typo can never leave an agent without tools.
func ProfileFromEnv() Profile {
	p, err := ParseProfile(os.Getenv(ProfileEnvVar))
	if err != nil {
		return ProfileFull
	}
	return p
}

// liveInterceptTools hold a real browser socket open while the agent decides
// what to do with it. An LLM round trip is seconds; a browser's connect timeout
// is not. They stay registered in `full` — a human-driven agent that knows what
// it is doing can still use them — but they are deprecated over MCP and left
// out of every curated profile, which steers agents to the replay tools
// (send_request, start_intruder) that cannot stall live traffic.
var liveInterceptTools = []string{"forward_request", "drop_request", "forward_response", "drop_response"}

// liveInterceptDeprecation is appended to the packet-holding tool descriptions
// so the deprecation travels with the tool instead of living only in the docs.
const liveInterceptDeprecation = "DEPRECATED over MCP: the held packet keeps a real browser socket open until you answer, " +
	"and an agent round trip (seconds) outlasts the browser's read timeout, so the page stalls or errors. " +
	"Still served by the full profile for a human-supervised hold; left out of every curated profile. " +
	"Prefer replay, which cannot stall live traffic: send_request to re-issue one request, start_intruder to vary it."

// liveInterceptWarning is appended to the interception toggles. They do not
// hold a packet themselves, but switching interception on is what makes the
// hold queue fill, so the same latency problem starts here.
const liveInterceptWarning = "Switching this on holds live traffic until something answers each hold, " +
	"so an agent's round trip becomes the browser's wait; prefer send_request / start_intruder for agent-driven work."

// webPentestTools is the replay-first web workflow. It covers every tool named
// by the connect instructions' SETUP / AUTH / RECON / SCAN / RECORD sections, so
// an agent can follow the documented workflow end to end without reaching for a
// tool the profile does not serve.
var webPentestTools = []string{
	// setup and scope
	"check_readiness", "scope_from_url", "list_scope", "ca_info",
	// triage
	"list_flows", "get_flow", "analyze_flow", "host_stats",
	// replay and attack
	"send_request", "start_intruder", "intruder_state",
	// passive scan
	"run_scanner", "list_issues",
	// identities and authorization
	"promote_flow_to_authz", "add_authz_identity", "list_authz", "authz_run",
	"authz_differential", "cross_host_token_replay",
	"set_login_macro_from_flow", "run_login_macro",
	// blind callbacks
	"oob_new", "oob_state",
	// recording the work
	"create_finding", "update_finding", "add_finding_poc", "add_finding_image",
	"render_flow_preview", "list_findings",
	// asking the operator
	"request_human_input", "get_human_response",
}

// reportingTools is the pass over findings that already exist: close gaps, tidy
// targets, score, render evidence, export. It deliberately cannot create a
// finding — a reporting agent should not be filing new ones — but it can update
// one, because the readiness gaps it reads are not actionable otherwise.
var reportingTools = []string{
	"check_readiness", "finding_readiness",
	"list_findings", "get_finding", "update_finding",
	"preview_finding_targets", "normalize_finding_targets",
	"evaluate_finding_cvss", "redact_value",
	"render_evidence", "render_flow_preview",
	"export_report", "export_full_project",
	"request_human_input", "get_human_response",
}

// mobileTools is device interception setup. list_flows and check_readiness are
// included because without them the agent cannot tell whether the setup worked.
var mobileTools = []string{
	"check_readiness", "scope_from_url", "ca_info", "detect_ssl_pinning",
	"android_status", "android_setup", "android_teardown",
	"ios_status", "ios_setup", "ios_install_ca",
	"ios_ssh_status", "ios_ssh_setup", "ios_ssh_install_ca",
	"list_flows",
	"request_human_input", "get_human_response",
}

// profileDescriptions are one-liners for the catalogue served by GET /api/mcp.
var profileDescriptions = map[Profile]string{
	ProfileFull:       "Every tool. The default; unchanged from releases before profiles existed.",
	ProfileWebPentest: "Replay-first web pentest: scope, flows, Repeater, Intruder, passive scan, authz, OOB, findings.",
	ProfileReporting:  "Report pass over existing findings: readiness, target cleanup, CVSS, evidence rendering, export.",
	ProfileMobile:     "Device interception setup: Android, iOS, CA install, SSL-pinning detection.",
}

// ProfileTools returns the tool names a profile exposes, in registration order
// for `full` and in declaration order for the curated profiles. The slice is a
// copy, so a caller cannot mutate the profile.
func ProfileTools(p Profile) []string {
	switch p {
	case ProfileWebPentest:
		return append([]string(nil), webPentestTools...)
	case ProfileReporting:
		return append([]string(nil), reportingTools...)
	case ProfileMobile:
		return append([]string(nil), mobileTools...)
	default:
		return New("http://127.0.0.1:1").ToolNames()
	}
}

// ProfileDescription returns the one-line description of a profile.
func ProfileDescription(p Profile) string { return profileDescriptions[p] }

// ProfileCatalog describes every profile and its size, for GET /api/mcp.
func ProfileCatalog() []map[string]any {
	out := make([]map[string]any, 0, len(Profiles()))
	for _, p := range Profiles() {
		out = append(out, map[string]any{
			"name":    string(p),
			"tools":   len(ProfileTools(p)),
			"default": p == ProfileFull,
			"desc":    ProfileDescription(p),
		})
	}
	return out
}

// visibleSet reports which registered tools the server's profile serves.
func (s *Server) visibleSet() map[string]bool {
	if s.profile == ProfileFull {
		out := make(map[string]bool, len(s.order))
		for _, n := range s.order {
			out[n] = true
		}
		return out
	}
	names := ProfileTools(s.profile)
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if _, ok := s.tools[n]; ok {
			out[n] = true
		}
	}
	return out
}

// visibleOrder returns the profile's tools in registration order, so tools/list
// and the schema hash are stable whatever order a profile declares its names in.
func (s *Server) visibleOrder() []string {
	if s.profile == ProfileFull {
		return s.order
	}
	visible := s.visibleSet()
	out := make([]string, 0, len(visible))
	for _, name := range s.order {
		if visible[name] {
			out = append(out, name)
		}
	}
	return out
}

// Profile returns the profile this server serves.
func (s *Server) Profile() Profile { return s.profile }

// findingFormatPointer replaces the full finding-writing contract in a reduced
// profile's tool descriptions. The contract itself is already sent once in the
// initialize instructions, so repeating all 9 KB of it inside create_finding and
// update_finding costs an eager client ~5k tokens for nothing.
const findingFormatPointer = "REQUIRED FORMAT: claim, risk, target, reproduction, evidence, fix, retest — " +
	"point-first, one short sentence per field, with a differential proof (baseline → action → result) and real evidence. " +
	"The complete contract is in this server's connect instructions and at " + version.LLMSFullURL + "; follow it exactly. "

// condenseDescription strips the duplicated finding-writing contract out of a
// tool description. Derived from the one guide constant, so the condensed form
// cannot drift from the full one.
func condenseDescription(desc string) string {
	if !strings.Contains(desc, findingFormatGuide) {
		return desc
	}
	return strings.ReplaceAll(desc, findingFormatGuide, findingFormatPointer)
}

// profileRefusal is returned when a caller names a real tool the active profile
// does not serve. tools/list has to be the truth about the surface, so the call
// is refused rather than quietly run — and the message says how to widen it.
func profileRefusal(name string, p Profile) error {
	return fmt.Errorf("tool %q is not in the %q MCP profile (%d tools). Reconnect with %s=full "+
		"(stdio) or POST /mcp?profile=full (HTTP) for the complete surface, or pick a profile that serves it: %s",
		name, p, len(ProfileTools(p)), ProfileEnvVar, strings.Join(ProfileNames(), ", "))
}

// ---- profile-aware connect instructions ----

// instructionSections are the connect instructions, split at the paragraph
// boundaries they already had. Concatenated in order they are byte-identical to
// what every release before profiles sent, which TestInstructionsStayHonest...
// asserts. A reduced profile drops any section that names a tool it does not
// serve, so the guidance never advertises a tool the agent cannot call.
func instructionSections() []string {
	return []string{
		sectionPreamble,
		sectionGuide,
		sectionSetup,
		sectionAuth,
		sectionRecon,
		sectionScan,
		sectionRecord,
		findingFormatGuide + "\n\n",
		sectionTagging,
		sectionHumanInput,
		sectionAskForFindings,
		sectionImprove,
	}
}

// formatGuideSection is the one section whose tool mentions are prose examples
// rather than a workflow the agent must be able to run, so it is gated on the
// finding tools instead of on every name it happens to mention.
func formatGuideSection() string { return findingFormatGuide + "\n\n" }

// includedSections returns the instruction sections a profile keeps. A section
// is kept when every registered tool name it mentions is visible; the finding
// format guide is kept whenever a finding-writing tool is visible.
func includedSections(visible map[string]bool, known map[string]bool) []string {
	guide := formatGuideSection()
	var out []string
	for _, sec := range instructionSections() {
		if sec == guide {
			if visible["create_finding"] || visible["update_finding"] {
				out = append(out, sec)
			}
			continue
		}
		keep := true
		for _, name := range toolNamesIn(sec, known) {
			if !visible[name] {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, sec)
		}
	}
	return out
}

// instructionsFor builds the initialize instructions for a profile. The full
// profile returns the unmodified text; a reduced profile gets a PROFILE: header
// naming the active surface and only the sections it can actually follow.
func instructionsFor(p Profile, visible map[string]bool) string {
	if p == ProfileFull {
		return mcpInstructions()
	}
	known := make(map[string]bool, len(fullRegistryNames()))
	for _, n := range fullRegistryNames() {
		known[n] = true
	}
	header := fmt.Sprintf("PROFILE: %s — this connection serves %d of %d tools (%s). "+
		"Reconnect with %s=full (stdio) or POST /mcp?profile=full (HTTP) for the complete surface. "+
		"Workflow guidance for tools this profile does not serve is omitted below.\n\n",
		p, len(ProfileTools(p)), len(fullRegistryNames()), ProfileDescription(p), ProfileEnvVar)
	return header + strings.Join(includedSections(visible, known), "")
}

// fullRegistryNames returns every registered tool name, profile-independent.
func fullRegistryNames() []string { return New("http://127.0.0.1:1").ToolNames() }

// toolNamesIn returns the known tool names mentioned in text. It splits on
// identifier boundaries, so "set_login_macro_from_flow" does not also count as
// a mention of "set_login_macro".
func toolNamesIn(text string, known map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if known[tok] && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	slices.Sort(out)
	return out
}
