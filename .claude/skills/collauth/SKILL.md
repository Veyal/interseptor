---
name: collauth
description: Rules for internal/collauth, the collection auth suite (OAuth2, JWT, digest, SigV4, mTLS) and how it must be wired into collexec.
---

- All token-endpoint and digest-probe traffic goes through the injected `Doer`; collexec must pass a scope-guarded sender-backed Doer, never the default http.Client.
- Every secret (client secret, password, token, signed JWT, Authorization value) is added to the `redact.Registry`; errors pass through `Manager.fail`. The registry ignores values shorter than 6 chars, so tiny tokens are not masked.
- `Token` JSON/String are redacted by design; never add a field that serializes the raw token.
- Auth runs after script and `{{var}}` resolution; an explicit Authorization header always wins.
- Auth-code state is one-shot with a 10 minute TTL; the callback handler is mounted by control on the control port and shows no token data.
- Digest without a nonce probes the URL once with no body; SigV4 supports header signing only (no presigned query).
- Wired: `collexec.Pipeline.Auth` runs after scripts, variables, cookies and the codec (`stepRun.applyAuth`); token/digest requests use `collexec.StepDoer`, bound to the step through the request context, so the scope policy, own-listener rule, dial guard and flow capture apply. Never construct a `collauth.Manager` without `Doer: collexec.StepDoer{}` (`TestCollectionStackHasOnlyGuardedSendSites`).
- Token cache key = `collection|env|identity|hash(auth config)`; `collrun.tokenStore` persists tokens in `ix_tokens` (local only, removed by the secret scrub). The base-target pin never applies to a token endpoint.
- Importers write snake_case fields (`grant_type`, `client_authentication`, `addTokenTo: queryParams`); `Config.f` aliases them. Control routes: `POST /api/collections/oauth/begin` (UI session only) and `GET /api/collections/oauth/callback`; the callback runs the exchange under the collection's scope policy via `StoreBackend.AuthContext`.
