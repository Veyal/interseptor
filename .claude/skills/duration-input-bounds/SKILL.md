---
name: duration-input-bounds
description: Validate operator-provided Interseptor time quantities before converting or multiplying them into Go time.Duration values.
---

# Duration input bounds

Use this when a JSON setting, stored value, CLI flag, or imported configuration becomes a
`time.Duration`.

Non-negative `int64` input is not automatically safe: multiplying hours, seconds, or milliseconds by
a duration unit can wrap. Compute an explicit maximum from `math.MaxInt64 / unit`, validate before
the multiplication, and reject an out-of-range value before any destructive or partial operation.

Validate again at the execution boundary, not only when saving through the API. Existing databases,
imports, or manual edits can contain values that bypassed current validation.

For destructive jobs, test a persisted `math.MaxInt64` value and assert both an error and unchanged
data. Also test that the write API rejects it with `400`.

Validate at reusable engine boundaries too. For example, Intruder rejects negative or overflowing
millisecond delays in `Engine.Start` so direct callers cannot bypass the REST/UI contract and turn a
requested throttle into an immediate dispatch. Login-macro refresh seconds follow the same rule in
`sender.SetLoginMacro`; otherwise the seconds-to-duration multiplication can wrap and make every send
look overdue for refresh.
