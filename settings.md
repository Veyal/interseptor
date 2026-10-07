---
layout: default
title: Settings
classification: current
source: docs/settings.md
---
# Settings

Open **Configure → Settings**. Search matches both section names and their contents; **Escape**
clears the search. In a narrow window the sections wrap into a row of buttons; on a phone they form a grouped list.

## Network

| Section | Use it for |
|---|---|
| Proxy & network | Listener addresses, optional proxy username/password (off by default), system proxy, upstream connections and CA trust, capture policy, background/telemetry suppression, and Invisible proxy. |
| TLS / CA | Local CA trust, origin certificate verification, passthrough, and host-specific TLS handling. |
| Mobile devices | Android and iOS device connection and setup helpers. |

Most desktop setups use the local proxy at `127.0.0.1:8080` and control UI at `127.0.0.1:9966`.
Read the active listener summary before changing a port. A remote or mobile client needs the correct
network access; changing the address alone is not sufficient.

See [Proxy, TLS, and networking]({{ "/proxy-and-tls/" | relative_url }}) or [Mobile testing]({{ "/mobile-testing/" | relative_url }}) for
the connection-specific details.

## Testing

| Section | Use it for |
|---|---|
| Target scope | Include/exclude rules used by relevant History, intercept, and scanner controls. |
| Scanner & OOB | Enablement and configuration for the optional callback service. |
| Session / auth | Saved session headers and existing macro configuration. |

Passive scan controls live in **Recon → Scanner**. Upstream proxy CA trust is under
**Proxy & network → Upstream proxy → Advanced trust settings**, separate from origin verification.

Scope and session settings belong to the project. Review them after importing or switching
projects. The passive **Session inspector** in History is separate from **Session / auth**;
the inspector reads existing captures and does not refresh a session.

## System

| Section | Use it for |
|---|---|
| Project & data | Active project, saved projects, imports, exports, retention, and storage. |
| API & MCP | Client connections, API keys, network allowlist, and MCP contract information. |

For backups and migrations, choose the export format for the data you need to preserve.
A report, HAR, portable JSON, and a full project archive serve different purposes. See
[Projects and data]({{ "/projects-and-data/" | relative_url }}#export-formats).

The API & MCP panel exposes connection status and contract version/hash information for clients.
After an app update, reconnect clients that report stale capabilities. See [API & MCP]({{ "/api-and-mcp/" | relative_url }}).

## Save state and recovery

Settings show pending changes and save failures in place. Keep failed edits available, correct
invalid values, and use the displayed retry action. A background refresh should not be treated
as confirmation that an unsaved value reached the server.

Switching projects restarts the application and waits for the new project's identity. Pending
Notes, Findings, History notes, Settings changes, or revision restore can block the switch.
Use the project badge, the mobile **Project** action, or **Project & data** to enter the same
switching flow. See [Project boundaries]({{ "/projects-and-data/" | relative_url }}#project-boundaries).

## Appearance and shortcuts

The theme control lives in the top bar. **Jump to…** and **Ctrl/⌘+K** provide quick panel
navigation. Theme and presentation preferences are browser-wide; changing them does not change
the project's captured data.

