package activescan

// BuiltinTemplate returns default Starlark for a built-in active probe.
// Saving to ~/.interseptor/active-checks/<id>.star overrides the Go probe.
func BuiltinTemplate(id string) (string, bool) {
	t, ok := builtinStarlark[id]
	return t, ok
}

// IsBuiltinID reports whether id is a built-in active probe.
func IsBuiltinID(id string) bool {
	_, ok := builtinByID[id]
	return ok
}

// BuiltinMeta returns metadata for a built-in active probe.
func BuiltinMeta(id string) (Check, bool) {
	c, ok := builtinByID[id]
	return c, ok
}

var builtinByID = func() map[string]Check {
	m := make(map[string]Check, len(Checks))
	for _, c := range Checks {
		m[c.ID] = c
	}
	return m
}()

var builtinStarlark = map[string]string{
	"active-xss": `# Reflected XSS (built-in override).
def check(point, baseline, probe):
    marker = "xk7qz9m2"
    r = probe("<" + marker + ">")
    if marker and re_search("<" + marker + ">", r.body):
        if baseline.body == "" or re_search("<" + marker + ">", baseline.body) == None:
            return [finding("High", "Reflected cross-site scripting (XSS)",
                detail="Parameter " + point.name + " reflected unencoded.",
                evidence="reflected: <" + marker + ">",
                fix="HTML-encode output and set Content-Security-Policy.")]
    return []
`,
	"active-sqli-error": `# Error-based SQLi (built-in override).
def check(point, baseline, probe):
    for q in ("'", '"', "')", "'\""):
        r = probe(point.value + q)
        m = re_search('(?i)(SQL syntax|mysql_fetch|ORA-\\d{5}|PostgreSQL.{0,40}ERROR|SQLSTATE\\[)', r.body)
        if m and not re_search('(?i)(SQL syntax|mysql_fetch|ORA-\\d{5})', baseline.body):
            return [finding("High", "SQL injection (error-based)", evidence=m[:80],
                fix="Use parameterized queries.")]
    return []
`,
	"active-sqli-boolean": `# Boolean SQLi (built-in override — length-based).
def check(point, baseline, probe):
    if len(baseline.body) < 64:
        return []
    tru = probe(point.value + "' AND '1'='1")
    fls = probe(point.value + "' AND '1'='2")
    lb, lt, lf = len(baseline.body), len(tru.body), len(fls.body)
    if lt > 0 and (lt - lb if lt >= lb else lb - lt) <= lb // 20 + 8 and (lf - lt if lf >= lt else lt - lf) >= lt // 10 + 24:
        return [finding("High", "SQL injection (boolean-based)",
            evidence="true len=" + str(lt) + " baseline=" + str(lb) + " false len=" + str(lf),
            fix="Use parameterized queries.")]
    return []
`,
	"active-ssti": `# SSTI (built-in override).
def check(point, baseline, probe):
    r = probe(point.value + "{{7*7}}")
    if "49" in r.body and "49" not in baseline.body:
        return [finding("High", "Server-side template injection (SSTI)", evidence="{{7*7}} → 49",
            fix="Never embed user input in templates; use static templates.")]
    return []
`,
	"active-open-redirect": `# Open redirect (built-in override).
def check(point, baseline, probe):
    canary = "interseptor-oob.example"
    for pl in ("https://" + canary + "/x", "//" + canary + "/x"):
        r = probe(pl)
        if r.status >= 300 and r.status < 400:
            loc = r.header("Location")
            if loc and canary in loc:
                return [finding("Medium", "Open redirect", evidence="Location: " + loc,
                    fix="Allow-list redirect destinations.")]
    return []
`,
	"active-lfi": `# Path traversal / LFI (built-in override).
def check(point, baseline, probe):
    for payload in ("../../../etc/passwd", "..%2f..%2f..%2fetc%2fpasswd"):
        r = probe(payload)
        if re_search("root:.*:0:0:", r.body):
            return [finding("High", "Local file inclusion", evidence="passwd contents in response",
                fix="Normalize paths; deny .. segments.")]
    return []
`,
	"active-cmdi": `# Command injection (built-in override — timing-based; Starlark cannot
# measure delays, so this template uses ;id output as a weaker signal).
def check(point, baseline, probe):
    r = probe(point.value + ";id")
    if re_search("uid=\\d+\\(", r.body) and not re_search("uid=\\d+\\(", baseline.body):
        return [finding("High", "Command injection", evidence="id output in response",
            fix="Never pass user input to shell commands.")]
    return []
`,
	"active-crlf": `# CRLF injection (built-in override).
def check(point, baseline, probe):
    r = probe(point.value + "%0d%0aX-Injected:%20true")
    if r.header("X-Injected"):
        return [finding("Medium", "CRLF / header injection", fix="Strip CR/LF from input.")]
    return []
`,
	"active-sqli-time": `# Time-based blind SQLi (built-in override). Starlark cannot measure response
# time, so this template falls back to error/boolean signals; the Go probe does
# the real timing differential.
def check(point, baseline, probe):
    r = probe(point.value + "' AND SLEEP(0)-- -")
    m = re_search('(?i)(SQL syntax|mysql_fetch|ORA-\\d{5}|PostgreSQL.{0,40}ERROR|SQLSTATE\\[)', r.body)
    if m and not re_search('(?i)(SQL syntax|mysql_fetch|ORA-\\d{5})', baseline.body):
        return [finding("High", "SQL injection (time-based blind)", evidence=m[:80],
            fix="Use parameterized queries.")]
    return []
`,
	"active-nosql": `# NoSQL injection (built-in override — error-based).
def check(point, baseline, probe):
    sig = '(?i)(MongoError|MongoServerError|E11000 duplicate key|com\\.mongodb|BSONError|CastError|unexpected token.{0,20}in JSON)'
    if re_search(sig, baseline.body):
        return []
    for q in ("'", '"', "\\\\", "'||'1'=='1"):
        r = probe(point.value + q)
        m = re_search(sig, r.body)
        if m:
            return [finding("High", "NoSQL injection (error-based)", evidence=m[:80],
                fix="Type-check input; never pass raw input into query operators.")]
    return []
`,
	"active-ldap": `# LDAP injection (built-in override — error-based).
def check(point, baseline, probe):
    sig = '(?i)(javax\\.naming\\.|LDAPException|com\\.sun\\.jndi\\.ldap|LDAP: error code \\d+|Invalid DN syntax|Bad search filter)'
    if re_search(sig, baseline.body):
        return []
    for q in ("*)(&", "*))(|(cn=*", "*)(uid=*))(|(uid=*"):
        r = probe(point.value + q)
        m = re_search(sig, r.body)
        if m:
            return [finding("High", "LDAP injection (error-based)", evidence=m[:80],
                fix="Escape LDAP metacharacters (RFC 4515).")]
    return []
`,
	"active-xpath": `# XPath injection (built-in override — error-based).
def check(point, baseline, probe):
    sig = '(?i)(XPathException|Expression must evaluate to a node-set|xmlXPathEval|SimpleXMLElement::xpath|System\\.Xml\\.XPath|Empty Path Expression)'
    if re_search(sig, baseline.body):
        return []
    for q in ("'", '"', "']", "' or '1'='1"):
        r = probe(point.value + q)
        m = re_search(sig, r.body)
        if m:
            return [finding("High", "XPath injection (error-based)", evidence=m[:80],
                fix="Use parameterized XPath (variable binding).")]
    return []
`,
	"active-host-header": `# Host header injection (built-in override — X-Forwarded-Host point only).
def check(point, baseline, probe):
    if point.kind != "header" or point.name.lower() != "x-forwarded-host":
        return []
    canary = "interseptor-host.example"
    if canary in baseline.body:
        return []
    r = probe(canary)
    loc = r.header("Location")
    if (loc and canary in loc) or re_search("(?i)(https?:)?//" + canary, r.body):
        return [finding("Medium", "Host header injection (X-Forwarded-Host reflected into a URL)",
            evidence="X-Forwarded-Host canary reflected into a URL",
            fix="Build absolute URLs from a fixed canonical hostname; never trust Host/X-Forwarded-Host.")]
    return []
`,
	"active-cors-reflect": `# CORS misconfiguration (built-in override — Origin point only).
def check(point, baseline, probe):
    if point.kind != "header" or point.name.lower() != "origin":
        return []
    origin = "https://interseptor-cors.example"
    r = probe(origin)
    if (r.header("Access-Control-Allow-Origin") or "").lower() != origin:
        return []
    if (r.header("Access-Control-Allow-Credentials") or "").lower() == "true":
        return [finding("High", "CORS misconfiguration (arbitrary Origin reflected with credentials)",
            evidence="ACAO reflects " + origin + " with credentials",
            fix="Validate Origin against an allow-list; never reflect arbitrary origins with credentials.")]
    return [finding("Medium", "CORS misconfiguration (arbitrary Origin reflected)",
        evidence="ACAO reflects " + origin,
        fix="Validate Origin against a server-side allow-list.")]
`,
	"active-xxe": `# XXE (built-in override — body injection points only).
# Safe internal-entity canary only — no external entities (matches the Go probe).
def check(point, baseline, probe):
    if point.kind != "body":
        return []
    canary = "INTERSEPTOR_XXE_CANARY"
    payload = '<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe "' + canary + '">]><foo>&xxe;</foo>'
    r = probe(payload)
    if canary in r.body and canary not in baseline.body:
        return [finding("High", "XML external entity (XXE)",
            evidence="internal entity resolved: canary reflected",
            fix="Disable external entities in the XML parser.")]
    return []
`,
}
