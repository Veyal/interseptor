---
name: burp-migration-import
description: Add or maintain Interseptor migration from Burp Suite saved HTTP items without claiming support for Burp's undocumented native project persistence format.
---

# Burp migration imports

Use this when changing Burp-to-Interseptor migration.

## Supported boundary

PortSwigger documents saving selected HTTP items as XML, but does not publish native `.burp` project
files as an interchange format. Support **Save items** XML and return actionable guidance for native
project payloads. Do not guess or reverse-engineer an undocumented native format.

## Preserve traffic safely

- Stream with `encoding/xml`, retaining at most one `<item>` at a time.
- Decode the request and response according to each element's `base64` attribute.
- Split raw HTTP headers from bodies at the byte level so binary bodies are not newline-normalized.
- Reject malformed XML, invalid base64 attributes/data, and unresolved external entities.
- Reconstruct a URL only when the exported `<url>` is absent, and skip non-HTTP(S) or invalid URLs.
- Mark imported flows with `store.FlagImported`, preserve Burp comments as flow notes, invalidate the
  endpoint cache, and notify the UI after successful imports.
- Use only generic domains such as `example.com` and TEST-NET addresses in fixtures.

Cover the parser, REST persistence, binary bodies, cache invalidation, native-file guidance, route
catalog, and Settings journey with tests.
