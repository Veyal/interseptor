// Package httpfile imports .http / .rest files (the VS Code REST Client and
// JetBrains HTTP Client formats) into the collection model, returning the same
// Postman-v2.1-shaped postman.Result as the curl, Insomnia and Bruno
// importers. The input is treated as hostile text: nothing is executed,
// fetched or read from disk. Specifically:
//
//   - response-handler (`> {% %}`) and pre-request (`< {% %}`) scripts are kept
//     as inert, quarantined events marked dialect "jetbrains" and are never
//     run or translated;
//   - `< ./file` / `<@ ./file` / `> ./handler.js` references become
//     needs-asset report entries that record the path; the file is never read;
//   - request chaining ({{name.response.body.$.id}}) stays literal and is
//     reported needs-review, like Insomnia's {% response %} tags;
//   - environment variables (http-client.env.json, settings.json) are reported
//     as undefined references, never resolved.
//
// Bounds: input at most MaxInputBytes (8 MiB, ErrTooLarge), at most
// MaxRequests (5000) requests (the rest are dropped and reported blocked), a
// URL at most impkit.MaxURLLen, and at most MaxBodyBytes of body per request.
//
// Two dialects overlap heavily. The parser accepts the union and tags the
// collection with the detected dialect ("vscode", "jetbrains" or "common",
// see DetectDialect); the dialect only changes labelling, never what is parsed.
package httpfile

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Bounds.
const (
	MaxInputBytes = 8 << 20
	MaxRequests   = 5000
	MaxBodyBytes  = 4 << 20
)

// Errors.
var (
	ErrTooLarge = errors.New("httpfile: input exceeds 8 MiB")
	// ErrNotHTTPFile means the input is binary or holds no request.
	ErrNotHTTPFile = errors.New("httpfile: no HTTP requests found")
)

// Options tune a parse. NewID defaults to store.NewUID.
type Options struct {
	NewID func() string
	Name  string // collection name, default "HTTP file import"
}

// Dialects reported by DetectDialect.
const (
	DialectVSCode    = "vscode"
	DialectJetBrains = "jetbrains"
	DialectCommon    = "common"
)

// Parse imports every request found in an .http / .rest file. It returns the
// partially filled result together with ErrNotHTTPFile when nothing parsed.
func Parse(data []byte, opt Options) (*postman.Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	if opt.Name == "" {
		opt.Name = "HTTP file import"
	}
	res := &postman.Result{Kind: postman.KindCollection}
	res.Report.Format = "httpfile"
	if strings.IndexByte(string(data), 0) >= 0 {
		return res, ErrNotHTTPFile
	}
	text := normalize(string(data))
	dialect := DetectDialect(text)
	res.Collection = store.Collection{UID: opt.NewID(), Name: opt.Name, ScopePolicy: store.ScopePolicyBlock}
	c := &conv{res: res, opt: opt, dialect: dialect, defined: map[string]int{}, refs: map[string]string{}}
	c.run(text)
	c.undefined()
	side := postman.CollectionSidecar{Format: "httpfile", Extra: map[string]json.RawMessage{"dialect": impkit.MustJSON(dialect)}}
	res.Collection.Sidecar = impkit.MustJSON(side)
	impkit.Finish(&res.Report, "an HTTP file")
	res.Collection.ImportReport = impkit.MustJSON(res.Report)
	if res.Report.Stats.Requests == 0 {
		return res, ErrNotHTTPFile
	}
	return res, nil
}

// normalize strips a BOM and converts CRLF / CR to LF.
func normalize(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

var (
	reqLineRe   = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE) \S+`)
	sepRe       = regexp.MustCompile(`(?m)^###`)
	varDefRe    = regexp.MustCompile(`(?m)^@[^\s=]+\s*=`)
	tmplRe      = regexp.MustCompile(`\{\{[^{}\n]+\}\}`)
	directiveRe = regexp.MustCompile(`(?m)^(?:#|//)\s*@[A-Za-z]`)
	scriptRe    = regexp.MustCompile(`(?m)^[<>]\s*\{%`)
	responseRe  = regexp.MustCompile(`^HTTP/\d`)
)

const lookWindow = 1 << 20

// Looks reports whether data plausibly is an .http / .rest file. There is no
// magic header, so this is a heuristic: it needs a request line
// (`GET https://...`, method then a non-space token at column 0) AND at least
// one http-file marker: a `###` separator line, a `{{var}}` reference, an
// `@var =` definition, a `# @directive` comment or a `{% %}` script marker.
//
// False-positive risk: a Markdown document with `### Heading` lines and a
// fenced `GET /x` example, or prose that happens to start a line with a method
// word, can match; files containing a ``` fence are rejected to cut the
// common Markdown case. Raw HTTP captures (a bare request line plus headers)
// do not match unless they also contain `{{...}}` (templated captures) or
// `###`; raw responses (`HTTP/1.1 200 ...`) are rejected. Callers should try
// stricter format detectors first and treat this as a last resort.
func Looks(data []byte) bool {
	if len(data) > lookWindow {
		data = data[:lookWindow]
	}
	s := normalize(string(data))
	if strings.Contains(s, "```") {
		return false
	}
	first := strings.TrimSpace(s)
	if responseRe.MatchString(first) || strings.IndexByte(s, 0) >= 0 {
		return false
	}
	hasReq := false
	for _, ln := range strings.Split(s, "\n") {
		if reqLineRe.MatchString(ln) {
			hasReq = true
			break
		}
		if t := strings.TrimSpace(ln); strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			if ln == t && !strings.ContainsAny(t, " \t") {
				hasReq = true
				break
			}
		}
	}
	if !hasReq {
		return false
	}
	return sepRe.MatchString(s) || tmplRe.MatchString(s) || varDefRe.MatchString(s) ||
		directiveRe.MatchString(s) || scriptRe.MatchString(s)
}

var (
	jbSignals = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^[<>]\s*\{%`),
		regexp.MustCompile(`(?m)^>>!?\s+\S`),
		regexp.MustCompile(`(?m)^<>\s+\S`),
		regexp.MustCompile(`(?m)^>\s+\S+\.js\s*$`),
		regexp.MustCompile(`(?mi)^X-REQUEST-TYPE\s*:\s*GraphQL`),
		regexp.MustCompile(`\{\{\s*\$random\.`),
		regexp.MustCompile(`\{\{\s*\$uuid\s*\}\}`),
		regexp.MustCompile(`\{\{\s*\$env\.`),
		regexp.MustCompile(`(?m)^(?:#|//)\s*@(?:no-cookie-jar|no-log|prompt)\b`),
		regexp.MustCompile(`(?m)^(?:GRAPHQL|WEBSOCKET|GRPC)\s+\S`),
	}
	vsSignals = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^@[^\s=]+\s*=`),
		regexp.MustCompile(`\{\{\s*\$(?:guid|processEnv|dotenv|datetime|localDatetime|aadToken|randomInt)\b`),
		regexp.MustCompile(`(?m)^<@\S*\s+\S`),
		regexp.MustCompile(`\{\{\s*[\w-]+\.(?:response|request)\.`),
		regexp.MustCompile(`(?m)^(?:#|//)\s*@note\b`),
	}
)

// DetectDialect scores dialect-specific markers. JetBrains markers are
// script blocks, response redirects, `X-REQUEST-TYPE: GraphQL`, `$random.*`
// and `$uuid`; VS Code markers are `@var = value`, `$guid`/`$processEnv`/
// `$dotenv`/`$datetime`, `<@ file` and request-chaining expressions. A tie or
// no marker yields "common".
func DetectDialect(text string) string {
	jb, vs := 0, 0
	for _, re := range jbSignals {
		if re.MatchString(text) {
			jb++
		}
	}
	for _, re := range vsSignals {
		if re.MatchString(text) {
			vs++
		}
	}
	switch {
	case jb > vs:
		return DialectJetBrains
	case vs > jb:
		return DialectVSCode
	}
	return DialectCommon
}
