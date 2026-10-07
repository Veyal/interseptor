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
