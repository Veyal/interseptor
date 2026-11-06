---
id: export-and-report-leak
title: Export, report and download endpoints leak other tenants' data
applies_to:
  - path:/export
  - path:/download
  - path:/report
  - param:tenant
  - param:org_id
  - param:customer
severity_hint: High
tools:
  - list_flows
  - send_request
  - create_finding
evidence: >-
  One export requested with another tenant's identifier that returns their rows,
  plus the baseline export for your own tenant.
references:
  - OWASP API1:2023
  - CWE-639
---

# Export and report endpoints

Exports are frequently built as a second, simpler data path — a report job that
takes the tenant id from the request — and they are the least-tested way to read
another customer's records.

## Do it

1. Find every endpoint that produces a document: CSV/XLSX/PDF export, invoice,
   statement, audit log, activity feed, analytics summary.
2. Note how it selects data: a tenant/org/customer id in the path or query, a
   filter object in the body, or an async job whose id is guessable.
3. Request your own export, capture it as the baseline.
4. Swap the identifier for one belonging to another account you control (or one
   observed in a shared space) and request again.
5. Confirm the content, not the length: the file must contain rows that are not
   yours. A PDF of the right shape with the wrong tenant's name is enough.

## Evidence

Attach the baseline export request and the cross-tenant one, and quote a bounded
excerpt of the returned rows (redacted). Do not download a full production
export; range-limit or take the first page.

## Escalation

If the export takes a job id, check whether the job status endpoint also returns
the file for another id — that is the same finding with a second target.
