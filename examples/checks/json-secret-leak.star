# name: Leaked secrets in JSON response
# description: Flags response bodies whose JSON contains keys that look like secrets (password, secret, token, api_key).
# author: Interseptor
# version: 1.0.0
# severity: high
def check(flow):
    body = flow.res_body
    if not body or flow.mime and "json" not in flow.mime.lower():
        return []
    data = json_decode(body)
    if data == None:
        return []
    hits = []
    for key in data:
        lk = key.lower()
        if lk in ("password", "passwd", "secret", "api_key", "apikey", "token", "access_token", "private_key"):
            hits.append(key)
    if hits:
        return [finding(
            "high",
            "Secret-looking field in JSON response",
            detail="Response JSON exposes key(s) that typically hold secrets: " + ", ".join(hits),
            evidence=flow.path,
            fix="Never return secret material in API responses. Keep secrets server-side only.",
        )]
    return []
