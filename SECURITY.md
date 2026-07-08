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

Security fixes land on the **latest release** (and `main`). Interceptor is a single-maintainer
project — older releases are not backported; if you're on an older version, upgrade to the latest
release to pick up fixes.

## Self-update trust model

`interceptor update` downloads a prebuilt binary from the matching GitHub release and, when the
release includes a `checksums.txt`, verifies the downloaded binary's SHA-256 against it before
installing. That checksum is fetched from **the same GitHub release** as the binary itself, so this
defends against transport corruption and a tampered mirror/CDN — it does **not** independently
attest that the release itself is what the maintainer intended to publish (a compromised release
would ship a matching, equally compromised checksum). If you need a stronger integrity guarantee,
build from source instead of using a prebuilt binary or `interceptor update`.

## Windows CA-directory permissions

The local CA's private key directory (`~/.interceptor/ca/`) is created with restrictive POSIX-style
permissions (`0o700`) as defense in depth. On Windows/NTFS this request doesn't translate into a real
ACL restriction — Go's directory creation there doesn't map POSIX permission bits to NTFS ACLs, so
the directory is not meaningfully access-restricted at the OS level on that platform. Windows users
who need the CA private key protected from other local accounts should apply an explicit ACL to
`~/.interceptor/ca/` themselves (e.g. via `icacls`).
