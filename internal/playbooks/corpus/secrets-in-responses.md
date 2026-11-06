---
id: secrets-in-responses
title: Secret material exposed in responses, bundles and source maps
applies_to:
  - tech:api_key
  - tech:secret
  - tech:token
  - path:/static/
  - mime:application/json
severity_hint: High
tools:
  - list_flows
  - get_flow
  - create_finding
  - redact_value
evidence: >-
  The response (or bundle line) containing the key, plus what that key unlocks —
  a call made with it that should not have been possible.
references:
  - OWASP WSTG-CONF-05
  - CWE-200
---

# Secrets in responses

An API key in a JavaScript bundle is a prerequisite; it is a finding when you
show what it authorises. Keys that only drive client-side telemetry are noise.

## Look for

- Provider keys in bundles and source maps: `AIza`, `sk_live`, `AKIA`,
  `xox[baprs]-`, `ghp_`, `eyJ…` JWTs, and `-----BEGIN` private keys.
- Over-fetching JSON: fields the UI never shows (`apiKey`, `secret`,
  `webhookSecret`, `serviceAccount`, `internalToken`) returned to a normal user.
- Debug and admin endpoints left reachable: `/actuator/env`, `/debug/vars`,
  `/.env`, `/.git/config`, `/server-status`, source maps next to a bundle.
- Storage URLs with embedded credentials, or a presigned URL that grants more
  than the object it was issued for.

## Do it

1. Search History (`list_flows` with a search term, or a saved search) rather
   than re-fetching every asset.
2. Confirm the value is live with one read-only call, scoped to what the key
   should not permit. Use the smallest possible action.
3. `redact_value` the secret before you write it anywhere; cite length and a
   digest so the report stays verifiable without publishing the key.

## Evidence

The response flow with the secret (redacted), and the call the key made
possible. If you did not use the key, say `NOT confirmed` and rate the finding
on what the exposure alone establishes.

## Do not

Do not paste live credentials into a finding, a note, or a shell history. Do not
spend money or change state with a provider key to "prove" it works.
