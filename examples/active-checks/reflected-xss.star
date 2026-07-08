# Reflected XSS (custom active check).
#
# Sends a unique marker into the current injection point and checks whether it
# comes back in the response body *without* HTML-escaping — a reflected XSS
# confirmation. The marker is wrapped in ASCII letters so it survives most
# input filters and is easy to spot verbatim.
#
# `probe(payload)` sends a REAL mutated request (recorded in History, session-auth
# applied, counts against the run's request budget).
#
# Copy into ~/.interseptor/active-checks/ and arm & run an active scan.

def check(point, baseline, probe):
    marker = "xk7qz9m2"
    r = probe("<" + marker + ">")
    # If the raw <marker> survives (angle brackets intact), the value is reflected
    # unescaped — an attacker could swap the marker for a <script> payload.
    if marker != "" and re_search("<" + marker + ">", r.body):
        # Distinguish a real reflected-XSS from a templated/honeypot echo: only
        # treat it as High if the baseline did NOT already contain the marker.
        if baseline.body == "" or re_search("<" + marker + ">", baseline.body) == None:
            return [finding(
                "High",
                "Reflected XSS (custom)",
                detail="The " + point.kind + " parameter `" + point.name + "` is reflected into the response with HTML-special characters intact. An attacker can inject arbitrary markup/JavaScript.",
                evidence="reflected marker: <" + marker + ">",
                fix="HTML-encode the value on output (context-aware encoding for HTML/attribute/JS), and enforce a Content-Security-Policy as defense-in-depth.",
            )]
    return []
