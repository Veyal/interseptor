---
id: jwt-claims
title: JWT signature, algorithm and claim handling
applies_to:
  - header:authorization
  - body:eyJ
  - tech:jwt
severity_hint: High
tools:
  - decode
  - send_request
  - create_finding
evidence: >-
  A request carrying a token the server should reject (bad signature, alg=none,
  expired, or a tampered claim) that is accepted and reaches protected data.
references:
  - OWASP WSTG-SESS-10
  - CWE-347
---

# JWT handling

Three failures matter: the signature is not verified, the algorithm is trusted
from the token itself, and a claim is trusted without re-checking the database.

## Do it

1. Decode the token (`decode` with `jwtdecode`) and write down `alg`, `kid`,
   `iss`, `aud`, `sub`, `role`, `exp`.
2. Break the signature first: flip one base64 character. If the request still
   succeeds, verification is absent — a Critical finding on its own.
3. Try `alg: none` (and `None`, `NONE`) with the signature removed.
4. If the server trusts `kid`, point it at a file (`/dev/null`, a known key) or
   an injection-shaped value; a `kid` used in a path or SQL is its own finding.
5. Then tamper claims inside a *valid* signature if you have a key you can
   control (a dev key, an RS256→HS256 confusion with the public key as the HMAC
   secret).
6. Expired and not-yet-valid tokens: re-send an expired one and a token with
   `exp` in the future that the server never checks.

## Evidence

Attach the modified request and the protected response, with the unmodified
request as baseline. Redact tokens with `redact_value` and cite length/digest.

## Do not

Do not brute-force a signing secret on a production target. One well-chosen
guess (a framework default) is fine; a wordlist run is out of scope unless the
engagement says otherwise.
