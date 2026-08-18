---
name: atomic-settings-batches
description: Persist related Interseptor settings atomically before applying the same configuration to live runtime components.
---

# Atomic settings batches

Use this when one control action changes multiple related `settings` rows and then updates an in-memory
engine, sender, proxy, or cache.

Build the complete key/value map first and call `Store.SetSettings`, which upserts the batch inside one
SQLite transaction. Only after it succeeds should the live component be changed or update events be
broadcast. On failure, use `httpInternalErr`; never ignore a persistence error and report success.

Marshal/validate every setting before opening the transaction so serialization failures cannot leave a
partial batch. Preserve omitted optional fields by leaving their keys out of the map.

Test the handler with a closed store, or install a SQLite trigger that rejects a later sorted key:
it must return a scrubbed `500`, retain every prior value, and must not follow the old best-effort
path that mutates runtime state after failed writes.

Background callbacks need the same contract. Return persistence errors to the runtime owner and apply
new in-memory state only after the callback succeeds; a `void` callback that discards `SetSetting`
errors silently creates a session that works until restart and then vanishes.
