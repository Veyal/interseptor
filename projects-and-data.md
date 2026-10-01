---
layout: default
title: Projects and data
classification: current
source: docs/projects-and-data.md
---
# Projects and data

## Choose a project

Open the project badge, the mobile **Project** action, or **Settings → Project & data** to choose
an existing project or create one. Named projects and explicitly opened folders are separate
workspaces, each with its own captured traffic and findings.

The current saved-project list is flat; it does not provide nested client folders or inherited
client-level settings. For related workspaces, use clear names such as `example-web-staging` and
`example-mobile-testing`. A naming convention groups related work for people; it does not merge
scope, session state, or evidence.

A project switch restarts Interseptor and waits for the new project to reconnect. Resolve unsaved
work first, and confirm the new project badge before continuing.

## Storage layout

The default project lives in `~/.interseptor/`. Named projects normally live in
`~/.interseptor/projects/<name>/`; an explicit path can place a project elsewhere. A project contains
SQLite metadata, content-addressed captured bodies, project settings, findings, and message codecs.
The CA and custom scanner checks are shared globally rather than copied into every project.

Captured data is not encrypted at rest. Protect the workstation, backups, exported reports, and
project archives as engagement evidence.

## Project boundaries

History, scope, rules, findings, notes, session settings, Repeater and Intruder drafts and presets,
Map view preferences, setup-wizard completion, and codecs are project-scoped. Presentation-only
preferences—such as theme, the last open top-level panel and Settings section, History columns,
and inspector height—remain browser-wide. Device-helper form state may remain in the current
browser tab.

Repeater and Intruder tab drafts and presets are synchronized with the project database and cached
in the browser under the project's full canonical directory. Older name-based browser keys migrate
only when they belong unambiguously to the active project; ambiguous, malformed, or oversized
drafts remain untouched and produce a recovery warning. Each Repeater or Intruder workspace accepts
up to 200 tabs, and each project-backed UI-state document is limited to 4 MiB of UTF-8 JSON. A
retained draft is replaced only after an explicit edit, and unavailable browser storage does not
block project-database synchronization.

If the project-list endpoint is temporarily unavailable but the version endpoint still identifies
the canonical directory, the workspace can operate from project storage. When an older unscoped or
name-keyed browser draft also exists, Interseptor does not guess its owner: migration or removal of
that legacy value is deferred while new edits continue to use the exact canonical-directory browser
key and synchronize with the project database. Unscoped state is migrated only when the project list
proves exactly one valid entry whose name matches the active project. Duplicate names and malformed
or blank entries are not ownership proof. The legacy value remains untouched for recovery. Reload after project
identity is fully available to retry the guarded migration.

A Repeater tab's send list is project-keyed but browser-local. It survives request edits, tab
switches, and reloads, and is deleted when that Repeater tab closes. The corresponding request and
response flows remain in the project store; project export and peer sync do not preserve their
browser-local tab grouping.

Switching projects restarts/re-executes the application so proxy and control listeners move to the
new store together. Finish unsaved edits and check the project badge before sending traffic.

Use one project per target or engagement boundary. This reduces accidental cross-target evidence,
session-header reuse, and report contamination.

## Export formats

| Format | Purpose | Behavior |
|---|---|---|
| Portable project JSON | Sharing selected operational state with another Interseptor instance | Imports additively into the current project; duplicate data is skipped. |
| HAR | Interchange with browser and proxy tooling | Imports flows into History; some Interseptor-only metadata is not represented. |
| Burp saved-items XML | Migrating Proxy history or Target traffic from Burp Suite | Imports request/response pairs, binary bodies, headers, timestamps, and Burp comments into History. |
| Postman collection JSON | Preparing editable requests in Repeater | Imports Collection v2.0/v2.1 with optional environment resolution; unsupported values are reported and no requests are sent by import. |
| Full project ZIP | Lossless migration or backup | Contains the database and captured bodies; import creates a new project. |
| Findings report | Client/editorial output | May include reconstructed PoC request/response bodies; treat as sensitive. |

The full archive intentionally excludes the global CA and custom checks. Transfer those separately
only when authorized and necessary.

### Migrate traffic from Burp Suite

In Burp, select the HTTP items to migrate in Proxy history or the Target tool, use **Save items**, and
save the XML export. In Interseptor, open **Settings → Project & data → Import Burp XML**. The import
merges valid HTTP/HTTPS items into the current project's History and keeps existing flows.

Native `.burp` project files are not accepted. PortSwigger documents project-file management but does
not publish the native persistence format as an interchange format; exporting selected items as XML
is the supported migration boundary. Keep exports protected as engagement evidence, and review the
reported imported/skipped counts before deleting the Burp project.

## Retention and deletion

Automatic retention can enforce a maximum age, maximum flow count, or both. The job runs periodically;
**Run now** applies it immediately. Host purge and “keep only” are destructive.

Flow deletion removes metadata first. Content-addressed bodies may still be referenced by another
flow, a finding screenshot, or another record. **Reclaim space** garbage-collects only unreferenced
body files. A finding that references a deleted flow preserves a missing-evidence marker, but the
request/response cannot be reconstructed; archive required PoC evidence before pruning.

## Backup and collaboration

For handoff, prefer a full project archive when exact bodies and report evidence must survive. Use
portable JSON or peer merge for additive collaboration. Preview merge counts, confirm the target
project, and review scope/session settings afterward.

The optional [Project vault]({{ "/vault/" | relative_url }}) stores revisioned project archives. It is not a replacement for
access control, encrypted disks, retention policy, or an engagement-approved backup location.

## Close-out

Export the required report and archive, verify each artifact opens, revoke API keys and tunnels,
remove client CA trust and proxy settings, then delete local evidence according to the engagement's
retention agreement. See [Engagement close-out]({{ "/engagement-closeout/" | relative_url }}).

