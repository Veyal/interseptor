---
layout: default
title: Message codecs
classification: current
source: docs/message-codecs.md
---
<p class="eyebrow">CURRENT</p>
# Message codecs

Project-scoped **Starlark** transforms that decrypt/encrypt (or otherwise decode/encode)
application-layer request and response bodies so History, Repeater, Intercept, and MCP
can show plaintext without leaving Interseptor.

This is **not** Content-Encoding decompression (gzip/br/…) and **not** the one-shot
Decoder tool (base64/url/hex/html/jwt). Codecs are for engagement-specific app crypto
(AES-wrapped JSON fields, custom token packing, …).

## Where codecs live

`<project>/codecs/*.star` — created automatically under the active project directory.
They are **not** shared across projects (unlike global scanner checks).

UI: **Scanner → Codecs**. REST under `/api/codecs` and `/api/flows/{id}/decoded`.
MCP: `list_codecs`, `test_codec`, `save_codec`, `delete_codec`, `get_flow_decoded`, `encode_codec`.

## Contract

```python
meta = {
    "id": "aes-content-field",
    "title": "JSON content (prefix+AES-ECB)",
    "apply_on_send": False,  # default: display-only
}

def match(flow, side):
    # side is "req" | "res"
    return True

def decode(flow, side, raw):
    # return plaintext string OR {"plaintext": "...", "fields": {...}, "note": "..."}
    return raw

def encode(flow, side, plaintext):
    # required only if apply_on_send is True
    return plaintext
```

`flow` matches the custom-check surface (`method`, `host`, `path`, `req_body` / `res_body`,
`req_header(n)`, …). See [custom checks]({{ "/custom-checks/" | relative_url }}).

## Builtins

Shared Starlark stdlib (`internal/starx`) plus AES-ECB helpers:

| Builtin | Notes |
|---|---|
| `aes_ecb_encrypt(key, plaintext)` | PKCS7 pad → AES-ECB → **base64** ciphertext |
| `aes_ecb_decrypt(key, ciphertext)` | base64 or raw → AES-ECB → plaintext string |
| `hash`, `hmac`, `b64*`, `json_*`, `re_search`, … | same as checks |

`key` may be 16/24/32 raw bytes **or** hex of that length (32/48/64 hex chars).

## Safety

- Codecs run only on explicit UI/API/MCP decode requests — **never** on the proxy hot path.
- Failures fall back to raw; forwarding is never broken by a bad codec.
- `apply_on_send` is opt-in. Repeater Send with Decoded view re-encodes only when that flag is set.

## Example

See [`examples/codecs/aes-content-field.star`](https://github.com/Veyal/interseptor/blob/main/examples/codecs/aes-content-field.star) for the
common `prefix + Base64(AES-ECB(JSON))` wire shape.

