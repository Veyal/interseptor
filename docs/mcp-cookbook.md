# MCP cookbook

Short recipes for agents driving Interseptor over MCP. Samples use only `example.com`.

## Rate limit finding

1. `start_intruder` with `attackType=repeat`, `threads=5`, `repeat=60`, target `https://example.com/api/login`.
2. `intruder_state` and note `runId` once the run finishes.
3. `render_evidence` with `kind=intruder-timeline`, `runId`, `findingId`, `role=result`, and a proof such as
   "first 429 at request #31; 30 of 60 returned 2xx". The timeline shows the first blocked request,
   burst boundary and any `Retry-After` or `X-RateLimit-*` values that were recorded.
4. Attach the 429 response as a flow (`add_finding_poc`) and a real screenshot if the UI shows the block.

## Account lockout finding

1. Run a sniper attack of wrong passwords against one account (`delayMs=200`, `threads=1`).
2. `render_evidence` with `kind=intruder-distribution` to show a late cluster of 423/429 with a distinct length,
   and `kind=intruder-strip` to show which payload index flipped. Credential-like payloads are masked.
3. Add a control: a correct-credential request after lockout and one against a different account.

## Race condition finding

1. `start_intruder` with `attackType=repeat`, `threads=N`, `barrier=true`, and `grepMatch` / `grepExtract`
   set to the success text or the value that should be unique.
2. `render_evidence` with `kind=intruder-race`. It plots the recorded launch spread and groups outcomes by
   status and body hash. The headline leads with the success pattern (`expected` adds the baseline) and
   says "N responses share extracted value ... check whether it should be unique". Extracted values are
   masked as `[len N #digest]` unless you pass `unmask`.
3. State the claim in the proof as counts only. The render does not confirm a race, and the requests used
   separate connections, not single-packet synchronisation. Verify the state change with a normal request.

## Authz and sequences

- `kind=authz-matrix` from an authz differential run (`sourceRef=authz:<runId>`).
- `kind=flow-diff` for two flows, `kind=flow-waterfall` for an ordered flow sequence, `kind=finding-chain`
  for related findings.

All renders are `source=evidence_render`. They are generated, not browser proof.
