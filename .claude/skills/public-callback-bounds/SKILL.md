---
name: public-callback-bounds
description: Preserve strict memory and identifier bounds on Interseptor endpoints that intentionally accept unauthenticated target callbacks, especially the OOB catcher.
---

# Public callback bounds

Use this when changing `/oob/` handling or another unauthenticated callback surface.

## Bound entries and every retained field

An entry-count ring is not a memory bound when each entry retains attacker-controlled request
metadata. Validate callback identifiers against the exact minted format before recording, and cap
path, query, Host, remote address, User-Agent, and body preview independently.

Keep suffix paths after a valid token working because target callbacks may append route data. Do
not store arbitrary token strings: OOB tokens are exactly 16 lowercase hexadecimal characters.

Tests should cover invalid token formats, valid `/oob/<token>/suffix` extraction, and oversized
metadata in every retained field. Assert byte lengths in the stored interaction, not only the HTTP
response, because the risk is long-lived heap retention.
