---
name: ui-feature-removal
description: Safely remove an embedded-UI feature without deleting neighboring controls or leaving unresolved JavaScript references.
---

# Embedded UI feature removal

Use this when removing a feature from `internal/control/ui/`, especially when its code is interleaved with unrelated settings or handlers.

## Required checks

1. Identify the feature by symbols, DOM IDs, API paths, and module imports; do not delete solely by a broad line range.
2. Review every removed function and handler. Keep unrelated code even when it sits between feature-specific declarations.
3. Search the remaining UI for references to every removed symbol.
4. Add a contract test proving neighboring controls retain their function definitions, API payloads, and event handlers.
5. Run the JavaScript module graph/syntax test and the focused UI journey tests before the full Go gates.

## Regression pattern

A stale-state banner such as `Settings is stale — NAME is not defined` means data loading succeeded but rendering threw. Fix the missing symbol and audit subsequent calls in the same render path; the first `ReferenceError` may hide more missing functions.
