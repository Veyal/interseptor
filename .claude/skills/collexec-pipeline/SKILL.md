---
name: collexec-pipeline
description: Rules for internal/collexec, the single Step() pipeline every collection send (UI, runner, CLI, MCP, pm.sendRequest) must go through.
---

# Collection execution pipeline

- One path: never add a second "send a collection item" implementation. UI, runner, CLI, MCP and `pm.sendRequest` call `Pipeline.Step`.
- `collexec` imports no script engine. Scripts go through the `Executor` interface; with no executor wired they are reported as skipped, never passed. A script the engine cannot run is `unsupported`, never `fail`/`pass`.
- Trust gate: hash input is `"<listen>\n<source>"` plus caps, identical to `collection.EventSources`/`ScriptHash`. Untrusted scripts do not run; the request still does unless `FailOnQuarantine`. A nil `Trust` fails closed.
- Order: pre scripts (collection, folders, request) -> resolve -> auth -> cookies -> pin/scope/own-listener -> codec -> send -> test scripts -> declarative assertions. Auth runs after scripts and resolution; an explicit `Authorization` header wins.
- Scope policy: UI sends `warn`, every other source `block` (override via `StepInput.ScopePolicy` or the collection). Own-listener refusal is unconditional. Private destinations need an explicit include-scope (`HasIncludes()`) listing the host, enforced at dial time by `sender.IPGuard`. Redirects are followed in `Step` (not the sender) so scope is re-checked per hop.
- Capture is best effort: `OnFlow` recovers panics and ignores `PutFlowCtx` errors; a capture failure must never fail the send (test: `TestForwardingNeverBrokenByCaptureFailures`).
- Secrets: `StepResult` text is masked through `redact.Registry`; `VarChange.Value` is `json:"-"`; flow rows keep true wire bytes. Add a canary assertion to any test that adds result fields.
- Request model: `params_json` non-empty means the URL's own query is dropped (authoritative params); empty keeps the URL query verbatim. Query values are escaped minimally so payloads reach the wire as typed. Multipart file parts and body mode `file` are refused (no local path reads).
- Codec: `settings.codec` = `""` auto (apply_on_send + match on the plaintext body), `"off"`, or an id applied directly (item body is the plaintext; the codec's `match()` is skipped).
- Use example.com names only in fixtures; httptest loopback servers need a fake explicit scope or `ScopePolicy: "off"`.
