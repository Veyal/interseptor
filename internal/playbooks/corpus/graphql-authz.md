---
id: graphql-authz
title: GraphQL field-level authorization and introspection on mutations
applies_to:
  - path:/graphql
  - path:/api/graphql
  - header:application/graphql
  - body:__schema
  - tech:graphql
severity_hint: High
tools:
  - send_request
  - list_flows
  - create_finding
evidence: >-
  A query or mutation executed as a low-privilege identity that returns fields
  or performs actions reserved for another role.
references:
  - OWASP GraphQL Cheat Sheet
  - CWE-639
---

# GraphQL authorization

REST guards usually sit on the resolver for a *type*; GraphQL exposes every
field of it, so a protected entity often leaks through an unprotected field.

## Do it

1. Introspect (`{__schema{types{name fields{name}}}}`) — if introspection is
   open, you have the whole surface. If it is closed, harvest field names from
   the app's own bundle and from error suggestions.
2. For each type you can reach, ask for the fields that look sensitive:
   `email`, `phone`, `token`, `role`, `internalNotes`, `deletedAt`, `owner`.
3. Mutations are the higher-value target: `updateUser`, `changeRole`,
   `addMember`, `setPrice` — try them with your own id and a field you should not
   be able to set (`role: "admin"`, `tenantId: <other>`).
4. Try the same operation with the field renamed (`__typename` abuse), a
   fragment on another type, or a batched array of operations, if a per-field
   guard exists.
5. Check depth and cost: a deeply nested query that returns cross-object data is
   both a leak and a denial-of-service candidate.

## Evidence

Attach the request and the response that carries the field or the state change,
under the lower-privileged identity, with the baseline from the rightful one.

## Do not

Do not run a full schema dump against production if a single field proves the
point; note the introspection result as an observation, not as the finding.
