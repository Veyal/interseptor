---
id: object-authz
title: Object-level authorization on identifiers in the path and body
applies_to:
  - param:id
  - param:user_id
  - param:account_id
  - param:order_id
  - path:/api/v1/
  - path:/api/v2/
severity_hint: High
tools:
  - list_flows
  - send_request
  - authz_run
  - create_finding
evidence: >-
  Two identities, the same request, different responses. Capture the owner's
  200 and the other identity's 200 with *their* data in it, and note which
  identifier the server trusted.
references:
  - OWASP API1:2023 Broken Object Level Authorization
  - CWE-639
---

# Object-level authorization

Replace every identifier in the request — path segment, query value, JSON field,
form field, header — with one that belongs to a **second** account you control.
Authorization is broken when the response still returns data or performs an
action for the other object.

## Do it

1. Capture a request as identity A that reads or mutates a specific object.
2. Identify every object reference, including nested ones
   (`{"order":{"customer":{"id":…}}}`).
3. `authz_run` replays the captured endpoints under every saved identity; treat
   a `different` verdict with 2xx as a candidate, not as proof.
4. Confirm by hand with `send_request`: as identity B, request A's object and
   read the body. A 403/404 means the control held; a 200 with A's data means it
   did not.
5. Cover the *writer* endpoints too: PUT/PATCH/DELETE with A's id as B is the
   finding people miss, because the read path is often guarded and the write
   path is not.

## Evidence

Attach both flows: the owner's normal request (baseline) and the cross-account
request (action/result). The result must contain the other account's data or a
successful state change, not merely a 200 status.

## Variations worth one request each

- An identifier that is a UUID vs an integer: try the integer next to it.
- Filters and exports: `?user_id=`, `?customerId=`, `?scope=all`.
- Bulk endpoints: send a list containing an id you do not own; partial success
  is still a finding.
- Deleted or draft objects: `state=draft` may skip the ownership check.

## Do not

Do not enumerate ids at scale. One confirmed cross-account object, plus the
reasoning that any id works, is the finding; a scan is out of scope and noisy.
