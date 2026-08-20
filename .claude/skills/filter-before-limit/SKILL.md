---
name: filter-before-limit
description: Preserve complete per-scope histories when adding bounded list APIs, tab-specific views, or canonical endpoint filters.
---

# Filter before limit

Use this when a UI presents a scoped history from a bounded backend list, such as requests for one
Repeater endpoint.

- Push every predicate that defines the visible scope into the store query before `LIMIT`, cursor, or
  pagination is applied. Never fetch a bounded global page and then filter it in the browser.
- Define a canonical identity containing every dimension that separates scopes. HTTP endpoints need
  scheme, case-normalized hostname, normalized port, and queryless path; normalize explicit/default
  ports and bracketed IPv6 consistently on both sides.
- Keep an unfiltered API mode only when compatibility requires it. When any scoped-filter component is
  supplied, require the full component set and reject malformed values so a typo cannot broaden into
  global history.
- Bound caller-controlled limits and use slim list queries that omit large detail columns.
- Reproduce page-boundary regressions by inserting the wanted row, more unrelated rows than the
  default limit, and newer wanted rows. Assert that the scoped result still contains all wanted rows
  in the expected order and excludes near-matches for scheme, port, and path.
