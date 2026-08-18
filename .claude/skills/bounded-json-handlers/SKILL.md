---
name: bounded-json-handlers
description: Enforce strict endpoint-specific JSON body limits in Interseptor control handlers without accepting a valid prefix followed by oversized padding or extra values.
---

# Bounded JSON handlers

Use this when adding or changing a control-plane handler that accepts bounded JSON.

## Decode the complete bounded body

Do not rely on `json.Decoder.Decode` over a bare request body or `io.LimitReader`: the decoder may
return after one valid JSON value without proving that the request ended inside the limit. A
`LimitReader` also turns overflow into an apparently clean EOF. Do not discard errors from
`http.MaxBytesReader`, either.

Use the shared `decodeLimitedJSON(w, r, limit, &dst)` helper. It combines
`http.MaxBytesReader` with a second decode so it rejects:

- bodies larger than the endpoint limit with `413 Request Entity Too Large`;
- malformed JSON with `400 Bad Request`; and
- a valid first value followed by another JSON value with `400 Bad Request`.

Use `decodeLimitedJSONDisallowUnknownFields` for contracts that deliberately reject extra object
fields; it provides the same complete-body and size guarantees.

Use `decodeOptionalLimitedJSON` only when an empty body has defined default semantics. It still
rejects malformed, multiple-value, and oversized non-empty bodies instead of silently applying the
default action.

For bounded non-streaming formats that are validated after reading (for example HAR or raw UI-state
JSON), use `readLimitedBody`. It reads `limit+1` bytes so a valid document prefix plus truncated
padding returns `413` instead of being accepted at exactly the cap.

Test the bypass shape explicitly: a small valid JSON value followed by enough whitespace to exceed
the endpoint limit must return `413`. Keep this endpoint-specific limit below the control guard's
global request-body backstop when the parsed data has a tighter CPU or memory budget.
