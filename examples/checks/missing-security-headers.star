# name: Missing security headers (bundle)
# description: Flags HTTPS responses missing any of X-Frame-Options, X-Content-Type-Options, Content-Security-Policy, Referrer-Policy.
# author: Interseptor
# version: 1.0.0
# severity: medium
def check(flow):
    if flow.scheme != "https":
        return []
    missing = []
    if not flow.res_header("X-Frame-Options"):
        missing.append("X-Frame-Options")
    if not flow.res_header("X-Content-Type-Options"):
        missing.append("X-Content-Type-Options")
    if not flow.res_header("Content-Security-Policy"):
        missing.append("Content-Security-Policy")
    if not flow.res_header("Referrer-Policy"):
        missing.append("Referrer-Policy")
    if not missing:
        return []
    return [finding(
        "medium",
        "Missing security response header(s)",
        detail="Response is missing: " + ", ".join(missing),
        evidence=flow.path,
        fix="Set the standard defensive response headers (see OWASP Secure Headers Project).",
    )]
