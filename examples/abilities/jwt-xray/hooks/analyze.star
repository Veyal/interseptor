# Action handler: extract and decode JWTs from a captured flow (headers, path,
# reqBody, resBody) or an explicit input token, flagging alg=none and claim risks.

_B64URL_CHARS = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_".elems())
_JWT_PATTERN = "eyJ[A-Za-z0-9_-]{4,}\\.[A-Za-z0-9_-]{2,}\\.[A-Za-z0-9_-]*"
_SENSITIVE_KEYS = set([
    "password", "passwd", "pwd", "secret", "client_secret",
    "private_key", "api_key", "apikey", "ssn", "pin",
])

_WS_BYTES = set([9, 10, 13, 32])
_DIGITS = set(range(48, 58))
_DIGITS_NZ = set(range(49, 58))
_HEX_BYTES = set(list(range(48, 58)) + list(range(65, 71)) + list(range(97, 103)))
_ESC_SIMPLE = set([34, 47, 92, 98, 102, 110, 114, 116])  # " / \ b f n r t

def _field(obj, key, default):
    if obj == None:
        return default
    if type(obj) == "dict":
        return obj.get(key, default)
    return getattr(obj, key, default)

def _b64url_decode_utf8(seg):
    if not seg:
        return None
    clean = seg.rstrip("=")
    if "=" in clean:
        return None
    n = len(clean)
    if n < 2 or n % 4 == 1:
        return None
    for ch in clean.elems():
        if ch not in _B64URL_CHARS:
            return None
    std = clean.replace("-", "+").replace("_", "/")
    pad = (4 - (n % 4)) % 4
    if pad > 0:
        std = std + ("=" * pad)
    return b64decode(std)

def _is_valid_b64url_sig(seg):
    if seg == "":
        return True
    clean = seg.rstrip("=")
    if "=" in clean or len(clean) % 4 == 1:
        return False
    for ch in clean.elems():
        if ch not in _B64URL_CHARS:
            return False
    return True

def _is_json_object(s):
    if not s or len(s) > 32768:
        return False

    mode = "start"
    stack = []
    after_val = "done"
    esc = False
    u_rem = 0
    lit_bytes = []
    lit_idx = 0

    for b in s.elem_ords():
        if mode == "str":
            if u_rem > 0:
                if b not in _HEX_BYTES:
                    return False
                u_rem -= 1
                continue
            if esc:
                esc = False
                if b == 117:  # 'u'
                    u_rem = 4
                elif b not in _ESC_SIMPLE:
                    return False
                continue
            if b == 92:  # '\\'
                esc = True
                continue
            if b == 34:  # '"'
                mode = after_val
                continue
            if b < 32:
                return False
            continue

        if mode == "lit":
            if b != lit_bytes[lit_idx]:
                return False
            lit_idx += 1
            if lit_idx == len(lit_bytes):
                mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
            continue

        # Numeric states that close when a non-numeric delimiter arrives.
        if mode == "num_sign":
            if b == 48:
                mode = "num_lead0"
                continue
            if b in _DIGITS_NZ:
                mode = "num_int"
                continue
            return False

        if mode == "num_lead0":
            if b == 46:  # '.'
                mode = "num_frac_start"
                continue
            if b == 101 or b == 69:  # 'e' / 'E'
                mode = "num_exp_sign"
                continue
            if b in _DIGITS:
                return False
            mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
            # Fall through to handle delimiter byte b.

        elif mode == "num_int":
            if b in _DIGITS:
                continue
            if b == 46:
                mode = "num_frac_start"
                continue
            if b == 101 or b == 69:
                mode = "num_exp_sign"
                continue
            mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"

        elif mode == "num_frac_start":
            if b in _DIGITS:
                mode = "num_frac"
                continue
            return False

        elif mode == "num_frac":
            if b in _DIGITS:
                continue
            if b == 101 or b == 69:
                mode = "num_exp_sign"
                continue
            mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"

        elif mode == "num_exp_sign":
            if b == 43 or b == 45:  # '+' / '-'
                mode = "num_exp_start"
                continue
            if b in _DIGITS:
                mode = "num_exp"
                continue
            return False

        elif mode == "num_exp_start":
            if b in _DIGITS:
                mode = "num_exp"
                continue
            return False

        elif mode == "num_exp":
            if b in _DIGITS:
                continue
            mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"

        if b in _WS_BYTES:
            continue

        if mode == "start":
            if b == 123:  # '{'
                stack.append("obj")
                mode = "obj_key_or_end"
                continue
            return False

        if mode == "done":
            return False

        if mode == "obj_key_or_end":
            if b == 125:  # '}'
                stack.pop()
                if len(stack) == 0:
                    mode = "done"
                else:
                    mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
                continue
            if b == 34:
                mode = "str"
                after_val = "obj_colon"
                continue
            return False

        if mode == "obj_key":
            if b == 34:
                mode = "str"
                after_val = "obj_colon"
                continue
            return False

        if mode == "obj_colon":
            if b == 58:  # ':'
                mode = "obj_val"
                continue
            return False

        if mode == "obj_comma_or_end":
            if b == 44:  # ','
                mode = "obj_key"
                continue
            if b == 125:  # '}'
                stack.pop()
                if len(stack) == 0:
                    mode = "done"
                else:
                    mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
                continue
            return False

        if mode == "arr_comma_or_end":
            if b == 44:  # ','
                mode = "arr_val"
                continue
            if b == 93:  # ']'
                stack.pop()
                mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
                continue
            return False

        if mode == "arr_val_or_end":
            if b == 93:  # ']'
                stack.pop()
                mode = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
                continue
            # Fall through to value dispatch.

        # Value dispatch for obj_val, arr_val, arr_val_or_end
        if b == 123:  # '{'
            if len(stack) >= 64:
                return False
            stack.append("obj")
            mode = "obj_key_or_end"
        elif b == 91:  # '['
            if len(stack) >= 64:
                return False
            stack.append("arr")
            mode = "arr_val_or_end"
        elif b == 34:  # '"'
            mode = "str"
            after_val = "obj_comma_or_end" if stack[-1] == "obj" else "arr_comma_or_end"
        elif b == 45:  # '-'
            mode = "num_sign"
        elif b == 48:  # '0'
            mode = "num_lead0"
        elif b in _DIGITS_NZ:
            mode = "num_int"
        elif b == 116:  # 't' -> true
            lit_bytes = [114, 117, 101]
            lit_idx = 0
            mode = "lit"
        elif b == 102:  # 'f' -> false
            lit_bytes = [97, 108, 115, 101]
            lit_idx = 0
            mode = "lit"
        elif b == 110:  # 'n' -> null
            lit_bytes = [117, 108, 108]
            lit_idx = 0
            mode = "lit"
        else:
            return False

    return mode == "done" and len(stack) == 0

def _find_candidates(text):
    out = []
    if not text:
        return out
    rem = text
    for _ in range(32):
        m = re_search(_JWT_PATTERN, rem)
        if m == None:
            break
        out.append(m)
        idx = rem.find(m)
        if idx < 0:
            break
        rem = rem[idx + len(m):]
    return out

def _max_sev(cur, candidate):
    rank = {"info": 0, "low": 1, "medium": 2, "high": 3}
    return candidate if rank.get(candidate, 0) > rank.get(cur, 0) else cur

def _decode_jwt(raw, sources, now_sec):
    parts = raw.split(".")
    if len(parts) != 3:
        return None
    if not _is_valid_b64url_sig(parts[2]):
        return None
    hdr_raw = _b64url_decode_utf8(parts[0])
    pay_raw = _b64url_decode_utf8(parts[1])
    if not _is_json_object(hdr_raw) or not _is_json_object(pay_raw):
        return None

    header = json_decode(hdr_raw)
    payload = json_decode(pay_raw)
    if type(header) != "dict" or type(payload) != "dict":
        return None

    alg = str(header.get("alg", "") or "")
    alg_lower = alg.lower()
    typ = str(header.get("typ", "") or "")
    sig_present = len(parts[2]) > 0

    warnings = []
    flags = []
    severity = "info"
    expired = False
    missing_exp = False
    alg_none = False

    if alg_lower == "none" or alg == "":
        alg_none = True
        flags.append("alg-none")
        warnings.append("Header alg is 'none' (or missing): token signature is not verified")
        severity = _max_sev(severity, "high")
    elif not sig_present:
        flags.append("empty-signature")
        warnings.append("Signature segment is empty despite alg=" + alg)
        severity = _max_sev(severity, "high")

    embedded_keys = []
    for key_name in ("jku", "x5u", "jwk"):
        if key_name in header and header.get(key_name) != None:
            embedded_keys.append(key_name)
    if len(embedded_keys) > 0:
        flags.append("embedded-key-ref")
        warnings.append("Header embeds key parameter (" + ", ".join(embedded_keys) + "): test for SSRF or untrusted key injection")
        severity = _max_sev(severity, "medium")

    if "kid" in header and header.get("kid") != None:
        kid_str = str(header.get("kid"))
        if "../" in kid_str or "..\\" in kid_str or "'" in kid_str or ";" in kid_str or "|" in kid_str:
            flags.append("suspicious-kid")
            warnings.append("Header kid contains path/injection metacharacters")
            severity = _max_sev(severity, "medium")

    exp_val = payload.get("exp", None)
    if "exp" not in payload or exp_val == None:
        missing_exp = True
        flags.append("missing-exp")
        warnings.append("Missing exp claim: token has no expiration timestamp")
        severity = _max_sev(severity, "medium")
    elif type(exp_val) == "int" or type(exp_val) == "float":
        exp_sec = int(exp_val)
        if exp_sec < now_sec:
            expired = True
            flags.append("expired")
            warnings.append("Token expired (exp=" + str(exp_sec) + " < now=" + str(now_sec) + ")")
            severity = _max_sev(severity, "low")

    nbf_val = payload.get("nbf", None)
    if type(nbf_val) == "int" or type(nbf_val) == "float":
        nbf_sec = int(nbf_val)
        if nbf_sec > now_sec:
            flags.append("not-yet-valid")
            warnings.append("Token not valid yet (nbf=" + str(nbf_sec) + " > now=" + str(now_sec) + ")")
            severity = _max_sev(severity, "low")

    sensitive_found = []
    for k in payload.keys():
        if str(k).lower() in _SENSITIVE_KEYS:
            sensitive_found.append(str(k))
    if len(sensitive_found) > 0:
        flags.append("sensitive-claim")
        warnings.append("Unencrypted JWT payload contains sensitive-looking claim(s): " + ", ".join(sorted(sensitive_found)))
        severity = _max_sev(severity, "medium")

    return {
        "raw": raw,
        "source": sources[0] if len(sources) > 0 else "",
        "sources": sources,
        "alg": alg if alg else "(none)",
        "typ": typ,
        "header": header,
        "payload": payload,
        "signaturePresent": sig_present,
        "algNone": alg_none,
        "missingExp": missing_exp,
        "expired": expired,
        "severity": severity,
        "flags": flags,
        "warnings": warnings,
    }

def _collect_from_headers(headers, prefix, buckets):
    if type(headers) != "dict":
        return
    for name, val in headers.items():
        src = prefix + ":" + str(name)
        if type(val) == "list" or type(val) == "tuple":
            for item in val:
                if item != None:
                    buckets.append((src, str(item)))
        elif val != None:
            buckets.append((src, str(val)))

def run(ctx):
    if type(ctx) != "dict":
        return {"count": 0, "tokens": []}

    flow = ctx.get("flow")
    inp = ctx.get("input")
    if type(inp) != "dict":
        inp = {}

    now_override = ctx.get("nowMs", inp.get("nowMs", None))
    now_val = int(now_override) if type(now_override) in ("int", "float") else now_ms()
    now_sec = now_val // 1000

    buckets = []
    explicit_token = str(ctx.get("token", inp.get("token", "")) or "").strip()
    if explicit_token:
        buckets.append(("input", explicit_token))

    if flow != None:
        req_headers = _field(flow, "reqHeaders", None)
        if req_headers == None:
            req_headers = _field(flow, "req_headers", {})
        res_headers = _field(flow, "resHeaders", None)
        if res_headers == None:
            res_headers = _field(flow, "res_headers", {})
        req_body = str(_field(flow, "reqBody", None) or _field(flow, "req_body", "") or "")
        res_body = str(_field(flow, "resBody", None) or _field(flow, "res_body", "") or "")
        path = str(_field(flow, "path", "") or "")

        _collect_from_headers(req_headers, "req.header", buckets)
        if path:
            buckets.append(("req.path", path))
        if req_body:
            buckets.append(("req.body", req_body))
        _collect_from_headers(res_headers, "res.header", buckets)
        if res_body:
            buckets.append(("res.body", res_body))

    ordered_raws = []
    sources_by_raw = {}
    for src, text in buckets:
        for tok in _find_candidates(text):
            if tok not in sources_by_raw:
                ordered_raws.append(tok)
                sources_by_raw[tok] = [src]
            elif src not in sources_by_raw[tok]:
                sources_by_raw[tok].append(src)

    tokens = []
    for raw in ordered_raws:
        decoded = _decode_jwt(raw, sources_by_raw[raw], now_sec)
        if decoded != None:
            tokens.append(decoded)

    res = {
        "count": len(tokens),
        "tokens": tokens,
    }
    if flow != None:
        fid = _field(flow, "id", None)
        if fid != None:
            res["flowId"] = fid
        host = str(_field(flow, "host", "") or "")
        method = str(_field(flow, "method", "") or "")
        path = str(_field(flow, "path", "") or "")
        status = _field(flow, "status", 0)
        if host:
            res["flow"] = {
                "id": fid,
                "method": method,
                "host": host,
                "path": path,
                "status": status,
            }
    elif explicit_token and len(tokens) == 0:
        res["error"] = "Input did not contain a valid three-segment JWT with JSON header and payload"

    return res
