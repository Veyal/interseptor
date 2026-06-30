package scanner

// BuiltinTemplate returns the default Starlark source for a built-in passive check.
// Saving this to ~/.interceptor/checks/<id>.star overrides the compiled Go check.
func BuiltinTemplate(id string) (string, bool) {
	t, ok := builtinStarlark[id]
	return t, ok
}

// IsBuiltinID reports whether id is a built-in passive check.
func IsBuiltinID(id string) bool {
	_, ok := builtinByID[id]
	return ok
}

var builtinByID = func() map[string]BuiltinCheck {
	m := make(map[string]BuiltinCheck, len(BuiltinChecks))
	for _, b := range BuiltinChecks {
		m[b.ID] = b
	}
	return m
}()

// Starlark ports of built-in passive checks — editable overrides of the Go logic.
var builtinStarlark = map[string]string{
	checkPasswordInBody: `# Password field in request body (built-in override).
def check(flow):
    m = re_search('(?i)"?password"?\\s*[:=]\\s*"?[^"&\\s,}]{3,}', flow.req_body)
    if not m:
        return []
    sev = "high" if flow.scheme == "http" else "medium"
    return [finding(sev, "Password transmitted in request body", evidence=m[:80],
        fix="Submit credentials over HTTPS; keep bodies out of logs.")]
`,
	checkTokenInResp: `# JWT / token in response body (built-in override).
def check(flow):
    jwt = re_search("eyJ[A-Za-z0-9_-]{8,}\\.[A-Za-z0-9_-]{6,}\\.[A-Za-z0-9_-]{4,}", flow.res_body)
    if jwt:
        return [finding("high", "Session token leaked in response body", evidence=jwt[:48] + "…",
            fix="Use Secure, HttpOnly cookies instead of returning tokens in the body.")]
    tok = re_search('(?i)"(access_?token|token|session|secret|api_?key)"\\s*:\\s*"[^"]{8,}"', flow.res_body)
    if tok:
        return [finding("high", "Session token leaked in response body", evidence=tok[:64],
            fix="Return session tokens via Secure, HttpOnly cookies.")]
    return []
`,
	checkVerboseError: `# Verbose 5xx errors (built-in override).
def check(flow):
    if flow.status < 500:
        return []
    body = flow.res_body.lower()
    for needle in ("traceid", "trace_id", "stacktrace", "stack", "exception", " at "):
        if needle in body:
            return [finding("medium", "Verbose error discloses internal details",
                fix="Return generic errors to clients; log diagnostics server-side only.")]
    return []
`,
	checkSecurityHeaders: `# Missing security response headers (built-in override).
def check(flow):
    missing = []
    mime = (flow.mime or "").lower()
    ct = (flow.res_header("Content-Type") or "").lower()
    is_html = "text/html" in ct or "text/html" in mime
    if is_html and not flow.res_header("Content-Security-Policy"):
        missing.append("Content-Security-Policy")
    if flow.scheme == "https" and not flow.res_header("Strict-Transport-Security"):
        missing.append("Strict-Transport-Security (HSTS)")
    if is_html or "javascript" in mime:
        xcto = (flow.res_header("X-Content-Type-Options") or "").lower()
        if "nosniff" not in xcto:
            missing.append("X-Content-Type-Options: nosniff")
    if is_html:
        csp = (flow.res_header("Content-Security-Policy") or "").lower()
        if not flow.res_header("X-Frame-Options") and "frame-ancestors" not in csp:
            missing.append("X-Frame-Options / CSP frame-ancestors")
    if is_html and not flow.res_header("Referrer-Policy"):
        missing.append("Referrer-Policy")
    if not missing:
        return []
    sev = "low"
    for m in missing:
        if "Content-Security-Policy" in m or "Strict-Transport-Security" in m:
            sev = "medium"
            break
    return [finding(sev, "Missing security response headers", evidence="Missing: " + ", ".join(missing),
        fix="Send CSP, HSTS on HTTPS, nosniff, frame-ancestors, Referrer-Policy.")]
`,
	checkCorsWildcard: `# CORS wildcard (built-in override).
def check(flow):
    if flow.res_header("Access-Control-Allow-Origin") == "*":
        return [finding("medium", "Overly permissive CORS policy", evidence="Access-Control-Allow-Origin: *",
            fix="Replace * with an explicit allow-list of trusted origins.")]
    return []
`,
	checkCorsCreds: `# CORS with credentials (built-in override).
def check(flow):
    if (flow.res_header("Access-Control-Allow-Credentials") or "").lower() != "true":
        return []
    acao = flow.res_header("Access-Control-Allow-Origin")
    origin = flow.req_header("Origin")
    if acao == "*":
        return [finding("high", "CORS wildcard with credentials enabled",
            evidence="Access-Control-Allow-Origin: * | Access-Control-Allow-Credentials: true",
            fix="Never use * with credentials; allow-list origins.")]
    if origin and acao == origin:
        return [finding("high", "CORS reflects request Origin with credentials enabled",
            evidence="Access-Control-Allow-Origin: " + acao,
            fix="Validate Origin against a server-side allow-list.")]
    return []
`,
	checkInsecureCookie: `# Cookie missing Secure/HttpOnly (built-in override).
def check(flow):
    cookie = flow.res_header("Set-Cookie")
    if not cookie:
        return []
    lc = cookie.lower()
    if "secure" not in lc or "httponly" not in lc:
        return [finding("low", "Cookie set without Secure and HttpOnly", evidence=cookie[:80],
            fix="Set Secure; HttpOnly; SameSite on session cookies.")]
    return []
`,
	checkCookieSameSite: `# Cookie missing SameSite (built-in override).
def check(flow):
    cookie = flow.res_header("Set-Cookie")
    if cookie and "samesite" not in cookie.lower():
        return [finding("low", "Cookie missing SameSite attribute", evidence=cookie[:80],
            fix="Add SameSite=Strict or Lax to cookies.")]
    return []
`,
	checkCacheableAuth: `# Cacheable auth response (built-in override).
def check(flow):
    if not flow.res_header("Set-Cookie"):
        return []
    cc = (flow.res_header("Cache-Control") or "").lower()
    if "no-store" not in cc and "private" not in cc:
        return [finding("low", "Authenticated response may be cached",
            evidence="Set-Cookie present; Cache-Control: " + flow.res_header("Cache-Control"),
            fix="Add Cache-Control: no-store or private on auth responses.")]
    return []
`,
	checkVersionDisclosure: `# Server version disclosure (built-in override).
def check(flow):
    for h in ("Server", "X-Powered-By", "X-AspNet-Version"):
        v = flow.res_header(h)
        if v and re_search("\\d+\\.\\d+", v):
            return [finding("low", "Server software version disclosed", evidence=h + ": " + v,
                fix="Suppress version-bearing headers at the edge.")]
    return []
`,
	checkReflectedParam: `# Reflected parameter in HTML (built-in override — customize).
def check(flow):
    return []
`,
	checkDBError: `# SQL error in response (built-in override).
def check(flow):
    m = re_search('(?i)(SQL syntax|mysql_fetch|ORA-\\d{4,5}|PostgreSQL.{0,40}ERROR|SQLSTATE\\[|Unclosed quotation mark)', flow.res_body)
    if m:
        return [finding("high", "Possible SQL injection (DB error in response)", evidence=m[:80],
            fix="Use parameterized queries; return generic errors.")]
    return []
`,
	checkPrivateIP: `# Private IP disclosure (built-in override).
def check(flow):
    m = re_search('(?:127\\.0\\.0\\.\\d{1,3}|10\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}|192\\.168\\.\\d{1,3}\\.\\d{1,3})', flow.res_body)
    if m:
        return [finding("low", "Internal IP address disclosed", evidence=m,
            fix="Avoid echoing internal IPs to clients.")]
    return []
`,
	checkBasicAuth: `# HTTP Basic auth (built-in override).
def check(flow):
    auth = flow.req_header("Authorization")
    if not auth or not auth.lower().startswith("basic "):
        return []
    sev = "high" if flow.scheme == "http" else "low"
    return [finding(sev, "HTTP Basic authentication in use", evidence="Authorization: Basic …",
        fix="Prefer session cookies; enforce HTTPS if Basic is required.")]
`,
	checkSensitiveURL: `# Sensitive param in URL (built-in override).
def check(flow):
    m = re_search('(?i)[?&](access_?token|api_?key|token|session|password|secret|auth)=([^&\\s]{6,})', flow.path)
    if m:
        return [finding("medium", "Sensitive token or credential in URL", evidence=m[:80],
            fix="Pass credentials in body or Authorization header, not the URL.")]
    return []
`,
	checkMixedContent: `# Mixed content (built-in override).
def check(flow):
    if flow.scheme != "https":
        return []
    m = re_search('(?i)(?:src|href)\\s*=\\s*["\']?http://', flow.res_body)
    if m:
        return [finding("medium", "Mixed content: HTTPS page loads HTTP resource", evidence=m[:80],
            fix="Load all sub-resources over HTTPS.")]
    return []
`,
	checkOpenRedirect: `# Open redirect (built-in override — customize).
def check(flow):
    return []
`,
	checkDirListing: `# Directory listing (built-in override).
def check(flow):
    if "<a href=" in flow.res_body and re_search("(?i)<title>\\s*index of /", flow.res_body):
        return [finding("low", "Directory listing enabled",
            fix="Disable autoindex in the web server config.")]
    return []
`,
}
