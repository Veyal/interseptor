---
name: pm-sandbox
description: Rules for extending the Postman-compatible script sandbox (internal/pmsandbox, internal/scriptctx): adding pm.* APIs, shims, limits, and keeping "unsupported" distinct from pass/fail.
---

# pm.* script sandbox

- Layout: `internal/scriptctx` is types only (Input/Output data, no deps). `internal/pmsandbox` runs one script phase: `Run(ctx, Input) Output`. The pm shim is JavaScript in `prelude/NN_*.js` (concatenated in filename order into one closure) on top of a few Go host functions in `host.go`. The engine is only reachable through `internal/jsrt`; never import goja here.
- Host functions are the only bridge. They reach the prelude through `globalThis.__h`, which the prelude captures and deletes before user code runs. Add a Go host func only for things JS cannot do safely (hashing, AES, sends); keep logic in the prelude.
- Absent means absent. Do not stub an unsupported Postman API to make a script "work". Unknown `pm.*`/Chai members go through `strict()`/`wrapAssertion` and throw `UnsupportedError`, which surfaces as status `unsupported` naming the API. Never let that become pass or fail.
- Add an API in three places or the analyser drifts: the prelude, `pm_coverage.json` (analyser data), and a corpus case in `testdata/pm_corpus.json`. `TestAnalyzerAgreesWithRuntimeOnCorpus` fails if static and runtime verdicts disagree.
- Corpus cases are synthetic `example.com` idioms only. Never paste a real collection, token or host into `testdata`.
- Secrets: variable values listed in `Vars.Secret` are masked in console, test names/messages and errors (`finalize` -> `makeRedactor`), using both initial and final values, plus the caller's `Input.Scrub`. `Output.Vars/Changes/Request` are raw for the pipeline to commit and must never be logged or exported. Add a canary-string test for any new human-facing output.
- Default-deny capabilities (`scriptctx.Caps`): check them in the prelude with `need(cap, what)`; sends are also checked in `host.fnSend` (budget, scope check, sender nil). Network goes only through `Input.Sender`; `Input.ScopeCheck` runs before every send.
- Limits live in `Limits.withDefaults`. Guard every allocator a script can call (`repeat`, `padStart`, `Array(n)`, `Buffer.alloc`, typed arrays, random bytes) with `LIM.*`. Guards are plain wrapper functions sharing the original `prototype`, not Proxies, so `x instanceof Array` keeps working.
- goja has no heap cap. `watch.go` samples live heap and cancels the run on growth; it is process-wide and best effort until the worker subprocess (WP9) replaces it. Tests that allocate use `HeapGrowth` and short `Timeout`.
- jsrt `Eval` clears a stale interrupt, so the finalizer can read back tests and variable writes after a timeout. Keep the finalizer on its own context.
- Determinism: all time and randomness come from `Input.Clock`/`Input.Rand` (uuid, `Math.random`, `WordArray.random`, AES salts, `randomBytes`). Do not call `Math.random`/`Date.now` from Go.
- Prototype safety: variable stores are `Object.create(null)`; build returned objects with `setOwn` so a variable named `__proto__` is just data. Each run is a fresh runtime, so pollution cannot cross runs.
