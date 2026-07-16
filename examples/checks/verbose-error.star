# name: Verbose error in response
# description: Flags response bodies that look like stack traces or internal error dumps (a sign of verbose error handling).
# author: Interseptor
# version: 1.0.0
# severity: medium
TRACE_RE = "(?i)(traceback|at [a-z.$]+\\([a-zA-Z0-9_]+\\.java:\\d+\\)|\\bException in thread|\\bat /.*\\.php:\\d+|stack trace)"

def check(flow):
    if flow.status < 500:
        return []
    m = re_search(TRACE_RE, flow.res_body)
    if not m:
        return []
    return [finding(
        "medium",
        "Verbose error output",
        detail="A " + str(flow.status) + " response contains what looks like an internal stack trace / error dump: " + m,
        evidence=flow.res_body[:120],
        fix="Return generic error messages to clients; log full details server-side only.",
    )]
