# ADR 0003: Script trust model

Status: accepted (owner defaults, plan sections 11 and 13). Date: 2026-10-07.

- Imported, merged, vault-restored, MCP- or CLI-supplied scripts are QUARANTINED: no `ix_script_trust` row, so they do not run (requests still do).
- Trust is bound to sha256 of the exact source plus loaded libs plus capability set; any import/merge edit resets it. A human UI edit auto-trusts that edit.
- Trust can only be granted from an interactive UI session. AI/MCP/agents/archives can never trust. CLI: `--no-scripts`, or `--allow-scripts --trust-hash <pin>`.
- Capabilities are default-deny per collection; absent functions are not stubbed (no fs, process, env, DNS, sockets, fetch). The only egress is the proxied sender with the scope guard (`block` for runner/CLI/scripts/MCP, `warn` for interactive sends); own listeners, loopback, link-local, RFC1918 and 100.64/10 denied unless the exact host is in scope, enforced at dial time.
- `jsrt` (WP0) enforces the engine half: no ambient globals, wall-clock interrupt, stack/source/console/timer caps, per-script fresh runtime, injected clock/rand. Anything a script can reach must be passed through `Runtime.Set`.
- Defaults: runner persist `ask`, CLI `discard`; unresolved `{{vars}}` block the send; collection items skip global session headers.
- Residual: a trusted script is trusted code; heap exhaustion is only mitigated once the WP9 worker ships.
