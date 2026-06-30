package activescan

// BuiltinTemplate returns default Starlark for a built-in active probe.
// Saving to ~/.interceptor/active-checks/<id>.star overrides the Go probe.
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
    r = probe("https://evil.example/")
    if r.status >= 300 and r.status < 400:
        loc = r.header("Location")
        if loc and "evil.example" in loc:
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
	"active-cmdi": `# Command injection (built-in override).
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
	"active-xxe": `# XXE (built-in override — body injection points only).
def check(point, baseline, probe):
    if point.kind != "body":
        return []
    payload = '<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><foo>&xxe;</foo>'
    r = probe(payload)
    if re_search("root:.*:0:0:", r.body):
        return [finding("High", "XML external entity (XXE)", fix="Disable external entities in the XML parser.")]
    return []
`,
}
