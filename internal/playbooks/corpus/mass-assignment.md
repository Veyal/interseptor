---
id: mass-assignment
title: Mass assignment on user, profile and role updates
applies_to:
  - method:PATCH
  - method:PUT
  - path:/user
  - path:/profile
  - path:/account
severity_hint: High
tools:
  - list_flows
  - send_request
  - diff_flows
  - create_finding
evidence: >-
  An update request that sets a field the UI does not offer (role, tenant,
  verified, balance) and a read-back showing the value stuck.
references:
  - OWASP API3:2023
  - CWE-915
---

# Mass assignment

Profile updates frequently bind the whole request body to the model. The test is
to send fields the UI never shows and read them back.

## Do it

1. Capture a normal self-update (name, email, avatar) as the baseline.
2. Add one field at a time, in the body's own casing and shape:
   - privilege: `role`, `isAdmin`, `permissions`, `scopes`, `verified`, `active`
   - ownership: `tenantId`, `orgId`, `accountId`, `ownerUid`
   - money and state: `balance`, `credit`, `priceTier`, `plan`, `status`
3. Read the profile back with a separate request: the update response may lie,
   the read-back does not.
4. If a field is ignored, try a nested object (`{"user":{"role":"admin"}}`),
   a different case (`is_admin`), and a `.../roles` sub-resource that takes a
   plain POST.
5. Check whether the value is honoured by an *action*, not only stored: a stored
   `role: admin` that grants nothing is a Low; one that reaches the admin API is
   the finding.

## Evidence

Baseline update, the mass-assignment request, and the read-back showing the
field set. If it granted a capability, attach the request that exercised it.

## Do not

Leave a privileged account behind. Revert the field (or delete the account you
created) and note the reversal in the finding.
