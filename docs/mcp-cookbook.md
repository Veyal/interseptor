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

## Focused evidence image

Use this when the proof sits deep in a long response (an admin flag at line 400 of a user list, a
reflected payload near the end of an HTML page, an error string after a stack of headers).

1. `get_flow` and copy the exact, distinctive proof text, for example `"isAdmin":true` or the payload
   you sent. A common word such as `true` highlights every line.
2. `render_flow_preview` with `flowId`, `findingId`, `focus` (that text), `focusSide=res`, a `caption`
   such as "Highlighted: admin flag in a customer's response", `role=result` and a `proof`.
   Use `lines=res:400-440` instead when you know the line numbers, or `regex=true` for a pattern.
3. Read the result line `Focus: mode=focus, matches req=0 res=1.` With `findingId`, a focus that is not
   found is refused and nothing is attached: fix the text once and render again, or pass `mode=top`. If
   the result says the body was cut at 64 KiB, the text lies beyond the image: use `lines=` or a string
   nearer the start instead of retrying the same text.

`render_flow_preview` draws a form-urlencoded, base64 or codec-decoded body as readable text by default
(`decode=auto`; a notice band says what was decoded, and the result adds a `Decoded body:` line). Quote the
decoded text in `focus`, and `lines=` counts decoded lines. Pass `decode=raw` when the evidence is the
encoding itself (the ciphertext, the wire format).

The image keeps the response status line, collapses the headers, shows six lines around each match
with the match highlighted and numbers every line. It is still a generated `flow_preview`, not
browser proof.

## Authz and sequences

- `kind=authz-matrix` from an authz differential run (`sourceRef=authz:<runId>`).
- `kind=flow-diff` for two flows, `kind=flow-waterfall` for an ordered flow sequence, `kind=finding-chain`
  for related findings.

All renders are `source=evidence_render`. They are generated, not browser proof.
