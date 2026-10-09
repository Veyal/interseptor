---
layout: default
title: Workspace guide
classification: current
source: docs/workspace.md
---
# Workspace guide

The sidebar follows the same groups as the app: Capture, Test, Recon, Report, and Configure.
On a narrow window, use the tool picker to reach the same panels. The project badge identifies
the active workspace; check it before changing stored data.

## Find the right menu

| Group | Menu | What belongs here |
|---|---|---|
| Capture | Proxy | Captured HTTP history, filters, notes, request/response inspection, and WebSocket frames. |
| Capture | Intercept | Requests and responses currently held by the proxy. |
| Test | Repeater | Editable request tabs and their response history. |
| Test | Intruder | Configured request variations and run results. |
| Recon | Scanner | Passive observations that need review before becoming findings. |
| Recon | Map | Hosts, paths, and relationships derived from captured traffic. |
| Report | Findings | Evidence records, review state, revisions, and report export. |
| Report | Notes | The project's Markdown notebook. |
| Report | Activity | Recorded agent actions and their results. |
| Configure | Settings | Connections, scope, integrations, session settings, and project data. |

Use **Jump to…** in the top bar, or **Ctrl/⌘+K**, to find a panel. Context actions belong to
the selected item; global settings stay in [Settings]({{ "/settings/" | relative_url }}).

## Inspect captured traffic

Open **Proxy** and select a History row. The inspector shows the request and response for that
capture. Search and filters narrow the list without editing stored traffic. Notes and tags help
you find an observation later; see [History search]({{ "/history-search/" | relative_url }}).

**Copy as PNG** in the inspector (bottom dock) and the flow drawer (side dock) copies the selected
flow as one image, request above response, ready to paste into Notion or a report. It uses the same
server-rendered preview as Findings (`GET /api/flows/{id}/preview.png`) and follows the active UI
theme: light renders light, dark and high contrast render dark. Browsers only allow image copy on
HTTPS or `localhost`; over plain HTTP (for example a Tailscale `http://` address) the toast says so and
offers **Download** instead.

Response views show different representations of the same captured body. **Raw** preserves the
text representation, **Pretty** formats supported content, and **Render**
previews HTML. Available views depend on the response. Message codecs can add a **Decoded** view;
see [Message codecs]({{ "/message-codecs/" | relative_url }}).

Render is a sandboxed preview with scripts disabled. It does not reproduce a full interactive
browser session, authenticate to the origin, or prove that page scripts executed. Use original
browser or device captures when a finding depends on visible application behavior.

## Repeater response views

Each Repeater tab owns its request editor and send history. Selecting a previous response changes
what you inspect; switching tabs keeps responses associated with the correct request. The
response pane includes **Render** when the selected response is HTML, with the same sandbox
limitations as History.

Tab drafts are project-backed. The per-tab send list is browser-local: it survives reloads until
the tab closes, while the underlying flows remain in the project store. Exporting the project
does not preserve that browser-local grouping. See [Project boundaries]({{ "/projects-and-data/" | relative_url }}#project-boundaries).

## Session inspector

Select one or more History rows, open the context menu, and choose **Inspect session timeline**.
The inspector organizes existing captures chronologically. You can assign Anonymous, User,
Admin, or Unassigned labels to compare observations.

Cookie observations are redacted. Status codes, response fingerprints, redirects, and candidate
transitions describe the selected captures only. The view sends no requests and cannot establish
authentication success, MFA completion, browser cookie decisions, or hidden side effects.
See [Passive session inspection]({{ "/findings-and-reporting/" | relative_url }}#passive-session-inspection).

## WebSocket frames

Captured WebSocket connections expose sent and received frames in the Proxy inspector. Read the
frame direction and timestamp before comparing payloads. The WebSocket replay editor opens inline
in that inspector; captured frames and replay results retain their own context.

## Intercept

Intercept shows traffic held by the proxy, separate from completed History captures. Its enabled
state affects whether matching traffic waits for a decision. If a browser appears to stall,
check the held-item count and current intercept state before changing network settings.

## Scanner and Map

Scanner groups passive observations from captured responses. An alert is a review lead; it is
not a report-ready finding. Findings keep the reviewed claim and supporting evidence separately.

Map organizes observed hosts and endpoints in tree and graph views. Search, filters, and collapsed
groups affect what is visible; a sparse map does not establish that an application has no other
endpoints. Its content comes from the traffic available to the project.

## Findings

Open **Report → Findings** to search records and move between Overview, Evidence, Remediation,
and Review. You can inspect attached HTTP evidence inline, compare revisions, and recover a
deleted finding. The export dialog distinguishes Final reports from Drafts with remaining gaps.
See the [Findings guide]({{ "/findings-and-reporting/" | relative_url }}#read-and-edit-a-finding).

## Notes

Open **Report → Notes** for the project notebook. **Edit** accepts Markdown and pasted images;
**Preview** shows the formatted document. Watch the save status before leaving the project.

If autosave fails, the draft stays available with **Retry save**. Switching projects is blocked
while unresolved notes or other guarded drafts remain. Notes support investigation context;
use Findings for structured records that belong in a report.

## Activity

Open **Report → Activity** to review recorded external-agent actions. Filter by intent, open
flow-linked entries in History, or expand an entry to read its summary, result, and intent.
This is the recorded activity available to the project, not a complete audit of every action
outside Interseptor.

**Clear** permanently removes the project's activity after confirmation. Preserve required
records before clearing them.

## Decoder

Use **Decoder** from a request or response text selection in Proxy/History. Paste or load text,
choose a transformation, and inspect the separate output. Supported options include Base64,
URL, Hex, HTML, JWT, and Smart. You can copy the result or move it back into the input.

Decoder is a one-shot text utility. It does not modify captured evidence, verify JWT signatures,
or configure persistent Message codecs.

## Other workspace tools

Intruder manages request variations and results. Authorization tools compare configured identity
contexts. These tools can send new traffic, unlike History, Decoder, and passive session inspection.
Their results still require human review before being represented as confirmed findings.

Checks and rule packs extend passive analysis; Message codecs add project-specific body views.
The [feature index](https://github.com/Veyal/interseptor/blob/main/features.md) links to the available guides. For connection problems or
stale drafts, start with [Troubleshooting]({{ "/troubleshooting/" | relative_url }}).

