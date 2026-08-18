# Projects and data

## Storage layout

The default project lives in `~/.interseptor/`. Named projects normally live in
`~/.interseptor/projects/<name>/`; an explicit path can place a project elsewhere. A project contains
SQLite metadata, content-addressed captured bodies, project settings, findings, and message codecs.
The CA and custom scanner checks are shared globally rather than copied into every project.

Captured data is not encrypted at rest. Protect the workstation, backups, exported reports, and
project archives as engagement evidence.

## Project boundaries

History, scope, rules, findings, notes, session settings, UI drafts, and codecs are project-scoped.
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
| Full project ZIP | Lossless migration or backup | Contains the database and captured bodies; import creates a new project. |
| Findings report | Client/editorial output | May include reconstructed PoC request/response bodies; treat as sensitive. |

The full archive intentionally excludes the global CA and custom checks. Transfer those separately
only when authorized and necessary.

### Migrate traffic from Burp Suite

In Burp, select the HTTP items to migrate in Proxy history or the Target tool, use **Save items**, and
save the XML export. In Interseptor, open **Settings → Project & Data → Import Burp XML**. The import
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

The optional [Project vault](vault.md) stores revisioned project archives. It is not a replacement for
access control, encrypted disks, retention policy, or an engagement-approved backup location.

## Close-out

Export the required report and archive, verify each artifact opens, revoke API keys and tunnels,
remove client CA trust and proxy settings, then delete local evidence according to the engagement's
retention agreement. See [Engagement close-out](engagement-closeout.md).
