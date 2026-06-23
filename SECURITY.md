# Security Policy

Interceptor is a security-testing tool, so we take vulnerabilities **in Interceptor itself**
seriously.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.** Report it privately through GitHub's
private vulnerability reporting:

> **Security → Report a vulnerability** on this repository
> (<https://github.com/Veyal/interceptor/security/advisories/new>)

We aim to acknowledge a report within a few days and will coordinate a fix and a coordinated
disclosure with you.

## Scope

**In scope** — bugs in Interceptor that put *its own users* at risk, for example:

- the control plane being reachable cross-origin or via DNS rebinding,
- the proxy binding to a non-loopback interface without the explicit opt-in,
- a leak of captured traffic or a configured API key,
- remote code execution, path traversal, or memory-safety issues in the binary or UI.

**Out of scope** — using Interceptor to test *other* systems (that is its purpose), and any
weaknesses it *reports about a target application* you point it at.

## Supported versions

Interceptor is pre-1.0; security fixes land on the latest release and `main`.
