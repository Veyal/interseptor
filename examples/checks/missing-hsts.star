# Flag HTTPS responses that omit HSTS.
# Copy into ~/.interseptor/checks/ and Run scan.

def check(flow):
    if flow.scheme == "https" and not flow.res_header("Strict-Transport-Security"):
        return [finding(
            "medium",
            "Missing Strict-Transport-Security (HSTS)",
            evidence="(no HSTS response header)",
            fix="Send Strict-Transport-Security: max-age=63072000; includeSubDomains.",
        )]
    return []
