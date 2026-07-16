# name: JWT exposed in response
# description: Flags a JWT returned in the response body or an Authorization header, so its claims (exp/aud) can be reviewed.
# author: Interseptor
# version: 1.0.0
# severity: low
JWT_RE = "ey[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+"

def check(flow):
    where = []
    tok = re_search(JWT_RE, flow.res_body)
    if tok:
        where.append("body")
    auth = flow.res_header("Authorization")
    if auth:
        at = re_search(JWT_RE, auth)
        if at:
            tok = at
            where.append("Authorization header")
    if not where:
        return []
    return [finding(
        "low",
        "JWT exposed in response",
        detail="A JWT was returned in the " + " and ".join(where) + ". Review its claims (esp. exp/aud) and avoid shipping tokens to clients that don't need them.",
        evidence=tok[:40] + "…",
        fix="Prefer HttpOnly cookies for session tokens; never return tokens the client doesn't strictly need.",
    )]
