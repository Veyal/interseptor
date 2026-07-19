# Writing message codecs

Project-scoped **Starlark** transforms decrypt/encrypt (or otherwise decode/encode)
application-layer bodies so History, Repeater, Intercept, and MCP can show plaintext.

This is **not** Content-Encoding (gzip/br) and **not** the one-shot Decoder tool
(base64/url/hex/html/jwt). Codecs are for engagement-specific app crypto.

## Where codecs live

`<project>/codecs/*.star` — under the active project directory only (not shared across projects).

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

The file name (without `.star`) is the codec id. Prefer matching `meta.id` to the filename.

## The `flow` object

Same surface as custom checks: `method`, `scheme`, `host`, `port`, `path`, `status`, `mime`,
`req_body` / `res_body`, `req_header(name)`, `res_header(name)`, `query_param(name)`.

## Builtins

| Builtin | Notes |
|---|---|
| `aes_ecb_encrypt(key, plaintext)` | PKCS7 → AES-ECB → **base64** ciphertext |
| `aes_ecb_decrypt(key, ciphertext)` | base64 or raw → AES-ECB → plaintext |
| `hash`, `hmac`, `b64*`, `json_*`, `re_search`, … | same as checks |

`key` may be 16/24/32 raw bytes **or** hex of that length (32/48/64 hex chars).

## Safety

- Codecs run only on explicit UI/API/MCP decode — **never** on the proxy hot path.
- Failures fall back to raw; forwarding is never broken by a bad codec.
- `apply_on_send` is opt-in. Repeater Send with Decoded view re-encodes only when set.

## Tips

- Start with `match` narrow enough that only the encrypted endpoints hit the codec.
- Use **Test** against a selected History flow before Save.
- Put engagement secrets in the Starlark file (project-local) — never commit real secrets to shared repos.
