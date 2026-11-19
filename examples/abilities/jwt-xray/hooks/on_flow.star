# Passive flow.captured hook: tag flows carrying Bearer or cookie JWTs,
# and add "jwt-alg-none" when any JWT header specifies alg=none.
# Written purely with standard Starlark built-ins so invalid headers never fail.

_B64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
_B64_MAP = {ch: idx for idx, ch in enumerate(_B64_ALPHABET.elems())}
_JWT_CHARS = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-.".elems())
_TARGET_HEADERS = set(["authorization", "cookie", "set-cookie"])

def _field(obj, key, default):
    if obj == None:
        return default
    if type(obj) == "dict":
        return obj.get(key, default)
    return getattr(obj, key, default)

def _b64url_decode_ascii(seg):
    if not seg:
        return None
    clean = seg.rstrip("=")
    n = len(clean)
    if n < 2 or n % 4 == 1:
        return None
    vals = []
    for ch in clean.elems():
        v = _B64_MAP.get(ch, -1)
        if v < 0:
            return None
        vals.append(v)

    out = []
    for i in range(0, n, 4):
        rem = n - i
        a = vals[i]
        b = vals[i + 1]
        out.append(chr(((a << 2) | (b >> 4)) & 0xff))
        if rem >= 3:
            c = vals[i + 2]
            out.append(chr((((b & 0x0f) << 4) | (c >> 2)) & 0xff))
            if rem >= 4:
                d = vals[i + 3]
                out.append(chr((((c & 0x03) << 6) | d) & 0xff))
    return "".join(out)

def _looks_like_json_obj(text):
    if not text:
        return False
    trimmed = text.strip()
    return trimmed.startswith("{") and trimmed.endswith("}")

def _header_has_alg_none(hdr_text):
    compact = hdr_text.lower().replace(" ", "").replace("\t", "").replace("\r", "").replace("\n", "")
    needle = '"alg":"none"'
    start = 0
    for _ in range(4):
        pos = compact.find(needle, start)
        if pos < 0:
            return False
        prev_ok = pos > 0 and compact[pos - 1] in ("{", ",")
        after = pos + len(needle)
        next_ok = after < len(compact) and compact[after] in ("}", ",")
        if prev_ok and next_ok:
            return True
        start = pos + len(needle)
    return False

def _scan_header_value(text):
    found_jwt = False
    found_alg_none = False
    if not text:
        return (found_jwt, found_alg_none)

    pos = 0
    n = len(text)
    for _ in range(16):
        idx = text.find("eyJ", pos)
        if idx < 0:
            break
        end = idx
        for j in range(idx, n):
            if text[j] not in _JWT_CHARS:
                break
            end = j + 1
        candidate = text[idx:end]
        pos = end if end > idx + 3 else idx + 3

        parts = candidate.split(".")
        if len(parts) != 3:
            continue
        if len(parts[0]) < 4 or len(parts[1]) < 2:
            continue
        hdr_text = _b64url_decode_ascii(parts[0])
        pay_text = _b64url_decode_ascii(parts[1])
        if not _looks_like_json_obj(hdr_text) or not _looks_like_json_obj(pay_text):
            continue
        found_jwt = True
        if _header_has_alg_none(hdr_text):
            found_alg_none = True
    return (found_jwt, found_alg_none)

def _collect_header_values(headers):
    values = []
    if type(headers) != "dict":
        return values
    for k, v in headers.items():
        if str(k).lower() not in _TARGET_HEADERS:
            continue
        if type(v) == "list" or type(v) == "tuple":
            for item in v:
                if item != None:
                    values.append(str(item))
        elif v != None:
            values.append(str(v))
    return values

def on_flow(flow):
    if flow == None:
        return None
    req_headers = _field(flow, "reqHeaders", None)
    if req_headers == None:
        req_headers = _field(flow, "req_headers", {})
    res_headers = _field(flow, "resHeaders", None)
    if res_headers == None:
        res_headers = _field(flow, "res_headers", {})

    values = _collect_header_values(req_headers) + _collect_header_values(res_headers)
    if len(values) == 0:
        return None

    has_jwt = False
    has_alg_none = False
    for val in values:
        jwt_ok, alg_none = _scan_header_value(val)
        if jwt_ok:
            has_jwt = True
        if alg_none:
            has_alg_none = True

    if not has_jwt:
        return None
    tags = ["jwt"]
    if has_alg_none:
        tags.append("jwt-alg-none")
    return {"tags": tags}
