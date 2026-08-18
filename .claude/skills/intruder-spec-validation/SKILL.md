---
name: intruder-spec-validation
description: Preserve safe Intruder attack-spec validation when adding attack modes, aliases, insertion-point behavior, or payload-list handling.
---

# Intruder spec validation

Use this when changing `internal/intruder` attack types or job construction.

## Validate before building jobs

Normalize attack-type aliases to a closed set before the mode switch. Reject unknown values; the
job builder must not use a default branch that turns typos into a real attack mode.

For modes with one payload list per insertion point:

- validate every supplied list that corresponds to a real `§…§` position before indexing it;
- permit omitted later lists only where the mode deliberately falls back to marker baselines; and
- ignore surplus lists beyond the number of positions so unrelated empty data cannot suppress jobs.

Keep malformed-spec tests at the `Engine.Start` boundary to prove invalid API/MCP inputs return an
error without starting traffic or panicking. Add focused `buildJobs` tests for list-to-position
mapping and request-cap behavior.
