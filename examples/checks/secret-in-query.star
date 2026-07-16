# name: Secret in URL query string
# description: Flags tokens/API keys passed in the query string (logged in URLs, referers, browser history).
# author: Interseptor
# version: 1.0.0
# severity: medium
SECRET_PARAM_RE = "(?i)(api[_-]?key|secret|token|passwd|password|access[_-]?token)"

def check(flow):
    qs = flow.path
    i = qs.find("?")
    if i < 0:
        return []
    m = re_search(SECRET_PARAM_RE + "=([^&]+)", qs[i+1:])
    if not m:
        return []
    return [finding(
        "medium",
        "Secret material in query string",
        detail="A secret-looking parameter is passed in the URL query string, where it leaks into logs, Referer headers, and browser history.",
        evidence=m,
        fix="Send secrets in a request header or body, never the URL.",
    )]
