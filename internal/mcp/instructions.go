package mcp

import "github.com/Veyal/interseptor/internal/version"

// The connect instructions, one constant per paragraph. They were a single
// concatenated string literal; splitting them at the boundaries they already
// had lets a reduced MCP profile drop the paragraphs whose tools it does not
// serve without rewording anything. Concatenated in order, they are the string
// every release before profiles sent — asserted by instructionSections tests.
//
// Each constant ends with its own blank line except the last, exactly as the
// original concatenation did.

const sectionPreamble = "Interseptor — an AI web-pentest workspace; a human watches everything you do and can take over manually, so record your work as you go.\n\n"

const sectionGuide = "GUIDE: the complete agent guide (hard rules, workflow, finding-writing contract, REST and MCP reference) is plain text at " + version.LLMSFullURL + " (index: " + version.LLMSURL + "). Read it when you are unsure how to write a finding or call a tool, and follow it so every agent produces the same output.\n\n"

const sectionSetup = "SETUP: check_readiness (structured JSON blockers: OOB, scope, auth identities, login macro) → fix blockers → scope_from_url → ca_info + route traffic through proxy. Re-run check_readiness if list_flows/scans come back empty.\n\n"

const sectionAuth = "AUTH: list_flows tag=auth → promote_flow_to_authz (Surveyor, Admin, …) → authz_run inScope:true → set_login_macro_from_flow → run_login_macro (refresh CSRF).\n\n"

const sectionRecon = "RECON: run content discovery with a real tool (feroxbuster / gobuster / ffuf) pointed THROUGH this proxy so hits land in History — Interseptor has no built-in forced-browser. Then triage with list_flows / host_stats.\n\n"

const sectionScan = "SCAN: run_scanner (passive) → inspect list_issues and relevant flows → use send_request, start_intruder, authz_run, cross_host_token_replay, and oob_* deliberately for confirmation and blind callbacks.\n\n"

const sectionRecord = "RECORD: write findings point-first — create_finding with claim/risk/target fields and a three-part proof of concept → attach captured requests with add_finding_poc → add a real browser/device screenshot when it visually proves the claim, or use render_flow_preview for a clearly labeled HTTP preview. The proof of concept is always Differential proof, in this order: baseline (what the application normally does), action (what we changed to trigger the issue), result (what the response changed to and the impact). Say each part in plain language in the proof field. When a finding lists more than one affected target, repeat that proof for each affected target you tested and link it with that target's flow_ids or image_hashes. A target you did not test may be listed without its own proof; do not invent a request to fill it. Give every flow/image a proof statement and provenance. Be brief: one short sentence per field, no background, hedging or restated title (see HOUSE STYLE). Never file a wall of prose or paste raw HTTP into detail/evidence.\n\n"

const sectionTagging = "Everything you do is tagged AI. Pass optional `intent` on consequential tools.\n\n"

const sectionHumanInput = "HUMAN INPUT (Interseptor / target engagement only): Use request_human_input for scope ambiguity, destructive or high-blast-radius target actions (mass IDOR fuzz, Intruder against prod-like targets), auth/identity choices that change what gets tested, or anything that exceeds the operator's declared engagement authority. Do NOT use it for local machine/OS admin (sudo, Remote Login, package installs, SSH/Tailscale host setup), general coding/git/Cursor questions, or non-Interseptor tooling — ask in the normal chat UI, or stop and tell the human what local command to run.\n\n"

const sectionAskForFindings = "ASK FOR FINDINGS: when the operator asks you to triage history and file findings, read scope + list_findings (dedupe) + in-scope list_flows / list_issues, then file only evidence-backed findings in the canonical claim/risk/target/reproduction/evidence/fix/retest/review envelope. Attach captured flows and proof annotations; prioritize a real screenshot when available, otherwise use a labeled flow preview when it improves reviewability. Skip duplicates. Summarize filed / skipped / needs_verification.\n\n"

const sectionImprove = "IMPROVE INTERSEPTOR: this workspace is a tool under active development, separate from the target you are testing. If an Interseptor tool errors, returns something wrong, or is missing a capability you needed, report it (or ask the human to) at https://github.com/" + version.Repo + "/issues — include the tool name, what you expected, and what actually happened. Do not file issues about the target application there. If you need human input for something outside Interseptor, Do NOT route it through request_human_input — use the normal chat channel, or stop and tell the operator exactly which local command to run."
