# Content discovery (guided)

Interseptor intentionally has **no built-in forced-browse engine**. The old Discover
feature polluted Proxy History and AI agents over-relied on it. Recon still belongs
here — via **external tools pointed through the proxy**, then triage in History / Map.

## Operator flow

1. Set **scope** (include the target host).
2. Point the browser or tool at the proxy (`127.0.0.1:8080` by default).
3. Run feroxbuster / gobuster / ffuf (copy-paste from **Map → Discovery ▸**).
4. Triage in **Proxy** History and **Map** (403/404-only noise hidden by default; soft-404 clusters).

### Example commands

```bash
# feroxbuster through Interseptor
feroxbuster -u https://app.example.com -p http://127.0.0.1:8080 --dont-extract-links -t 20

# ffuf
ffuf -u https://app.example.com/FUZZ -w wordlist.txt -x http://127.0.0.1:8080 -mc all -fc 404
```

## Soft-404 policy (Map)

Map clusters endpoints whose latest response body matches a not-found signature
even when status is 200 (**soft-404**), separate from byte-identical response clusters.
Nothing is auto-deleted — collapse only.

## History policy

- Prefer tools that rate-limit and respect scope.
- Map’s **Hiding 403/404-only** filters dead forced-browse paths from the endpoint view
  (toggle off to see everything: `?hideNoise=0` on `/api/endpoints`).
- Legacy `FlagDiscovery` / DSC badges remain for old projects; new runs are ordinary proxied flows.

## MCP

RECON guidance tells agents to run a real forced-browser **through this proxy**, then
`list_flows` / `host_stats` — there is no `start_discovery` tool.

## Future (engine return)

A scope-aware, soft-404-calibrated engine with strict History policy may return later.
Until then, guided external tools are the supported path (issue #22).
