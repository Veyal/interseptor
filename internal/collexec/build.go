package collexec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/varstore"
)

// toVarRequest maps the model onto the resolver's request shape so one pass
// resolves URL, path vars, query, headers, body and auth with shared limits.
// GraphQL variables ride in Form (key "variables"); multipart text parts ride
// in Form too, rebuilt into Parts after resolution.
func toVarRequest(m *RequestModel) varstore.Request {
	r := varstore.Request{
		Method:   m.Method,
		URL:      m.URL,
		PathVars: m.PathVars,
		Query:    m.Query,
		Headers:  m.Headers,
	}
	switch m.Body.Mode {
	case BodyRaw:
		r.Body = m.Body.Raw
	case BodyURLEncoded:
		r.Form = m.Body.Form
	case BodyGraphQL:
		r.Body = m.Body.GraphQLQ
		r.Form = []varstore.KV{{Key: "variables", Value: m.Body.GraphQLVars}}
	case BodyFormData:
		for _, p := range m.Body.Parts {
			r.Form = append(r.Form, varstore.KV{Key: p.Key, Value: p.Value, Disabled: p.Disabled || p.Type == "file"})
		}
	}
	if len(m.Auth.Fields) > 0 {
		r.Auth = m.Auth.Fields
	}
	return r
}

// built is a fully assembled, resolved wire request (before cookies, scope and
// codec).
type built struct {
	Method      string
	URL         string
	Headers     []varstore.KV // enabled, in order
	Body        []byte
	BodyText    string // plaintext body for codec encoding (raw/graphql/urlencoded)
	Applied     []string
	Warnings    []string
	AuthApplied bool
	// AuthType/AuthFields carry the resolved auth for the collauth suite when
	// the pipeline has one (the built-in handling is then skipped).
	AuthType   string
	AuthFields map[string]string
}

// assemble builds the wire request from the resolved structured request.
func assemble(m *RequestModel, res varstore.Request, deferAuth bool) (*built, error) {
	b := &built{Method: strings.ToUpper(strings.TrimSpace(res.Method))}
	if b.Method == "" {
		b.Method = "GET"
	}
	u := strings.TrimSpace(res.URL)
	if u == "" {
		return nil, fmt.Errorf("request URL is empty")
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	if m.QueryFromParams {
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			u = u[:i]
		}
		u = appendQuery(u, res.Query)
	}
	for _, h := range res.Headers {
		if h.Disabled || strings.TrimSpace(h.Key) == "" {
			continue
		}
		b.Headers = append(b.Headers, varstore.KV{Key: h.Key, Value: h.Value})
	}
	if err := buildBody(b, m, res); err != nil {
		return nil, err
	}
	if deferAuth {
		b.AuthType, b.AuthFields = m.Auth.Type, res.Auth
	} else {
		applyAuth(b, &u, m.Auth, res.Auth)
	}
	b.URL = u
	pu, err := url.Parse(b.URL)
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return nil, fmt.Errorf("invalid request URL")
	}
	return b, nil
}

func hasHeader(hs []varstore.KV, name string) bool {
	for _, h := range hs {
		if strings.EqualFold(h.Key, name) {
			return true
		}
	}
	return false
}

func defaultContentType(language string) string {
	switch language {
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "html":
		return "text/html"
	case "javascript":
		return "application/javascript"
	}
	return "text/plain"
}

func buildBody(b *built, m *RequestModel, res varstore.Request) error {
	ct := ""
	switch m.Body.Mode {
	case BodyRaw:
		b.BodyText = res.Body
		b.Body = []byte(res.Body)
		ct = defaultContentType(m.Body.Language)
	case BodyURLEncoded:
		b.BodyText = encodeForm(res.Form)
		b.Body = []byte(b.BodyText)
		ct = "application/x-www-form-urlencoded"
	case BodyGraphQL:
		vars := json.RawMessage("{}")
		if v := strings.TrimSpace(formValue(res.Form, "variables")); v != "" {
			if !json.Valid([]byte(v)) {
				return fmt.Errorf("graphql variables are not valid JSON")
			}
			vars = json.RawMessage(v)
		}
		payload, _ := json.Marshal(struct {
			Query     string          `json:"query"`
			Variables json.RawMessage `json:"variables"`
		}{res.Body, vars})
		b.BodyText, b.Body = string(payload), payload
		ct = "application/json"
	case BodyFormData:
		body, boundary, warn := encodeMultipart(m.Body.Parts, res.Form)
		b.Body, b.Warnings = body, append(b.Warnings, warn...)
		ct = "multipart/form-data; boundary=" + boundary
	case BodyFile:
		b.Warnings = append(b.Warnings, "body mode file is not supported: collections do not read local paths")
	}
	if ct != "" && !hasHeader(b.Headers, "Content-Type") {
		b.Headers = append(b.Headers, varstore.KV{Key: "Content-Type", Value: ct})
	}
	return nil
}

func formValue(form []varstore.KV, key string) string {
	for _, kv := range form {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}

func encodeMultipart(parts []Part, resolved []varstore.KV) ([]byte, string, []string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	var warn []string
	for i, p := range parts {
		if p.Disabled {
			continue
		}
		if p.Type == "file" {
			warn = append(warn, fmt.Sprintf("multipart file part %q skipped: collections do not read local paths", p.Key))
			continue
		}
		key, val := p.Key, p.Value
		if i < len(resolved) {
			key, val = resolved[i].Key, resolved[i].Value
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, escapeQuotes(key)))
		if p.ContentType != "" {
			h.Set("Content-Type", p.ContentType)
		}
		pw, err := w.CreatePart(h)
		if err != nil {
			continue
		}
		_, _ = pw.Write([]byte(val))
	}
	_ = w.Close()
	return buf.Bytes(), w.Boundary(), warn
}

func escapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "").Replace(s)
}

// queryEscape percent-encodes bytes that cannot appear in a query component
// but leaves existing %XX escapes and URL-safe punctuation alone, so payloads
// typed into a params table reach the wire as written.
func queryEscape(s string) string {
	const safe = "-._~!$'()*,;:@/?+"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(safe, c) >= 0:
			sb.WriteByte(c)
		case c == '%' && i+2 < len(s) && isHexByte(s[i+1]) && isHexByte(s[i+2]):
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func encodeForm(kvs []varstore.KV) string {
	var parts []string
	for _, kv := range kvs {
		if kv.Disabled || kv.Key == "" {
			continue
		}
		parts = append(parts, formEscape(kv.Key)+"="+formEscape(kv.Value))
	}
	return strings.Join(parts, "&")
}

// formEscape is application/x-www-form-urlencoded escaping (space -> +,
// '&', '=', '+' and '%' always encoded).
func formEscape(s string) string { return url.QueryEscape(s) }

func appendQuery(u string, kvs []varstore.KV) string {
	var parts []string
	for _, kv := range kvs {
		if kv.Disabled || kv.Key == "" {
			continue
		}
		parts = append(parts, queryEscape(kv.Key)+"="+queryEscape(kv.Value))
	}
	if len(parts) == 0 {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + strings.Join(parts, "&")
}

func authField(f map[string]string, name string) string { return f[name] }

// applyAuth adds credentials after resolution. An explicitly set Authorization
// header always wins. Unsupported types add a warning and send unmodified.
func applyAuth(b *built, u *string, a AuthModel, f map[string]string) {
	switch a.Type {
	case "", "none", "inherit":
		return
	case "basic":
		if hasHeader(b.Headers, "Authorization") {
			return
		}
		cred := base64.StdEncoding.EncodeToString([]byte(authField(f, "username") + ":" + authField(f, "password")))
		b.Headers = append(b.Headers, varstore.KV{Key: "Authorization", Value: "Basic " + cred})
	case "bearer":
		if hasHeader(b.Headers, "Authorization") {
			return
		}
		prefix := "Bearer"
		if p, ok := f["prefix"]; ok && p != "" {
			prefix = p
		}
		b.Headers = append(b.Headers, varstore.KV{Key: "Authorization", Value: prefix + " " + authField(f, "token")})
	case "oauth2":
		tok := authField(f, "accessToken")
		if tok == "" || hasHeader(b.Headers, "Authorization") {
			return
		}
		prefix := "Bearer"
		if p, ok := f["headerPrefix"]; ok && p != "" {
			prefix = p
		}
		b.Headers = append(b.Headers, varstore.KV{Key: "Authorization", Value: prefix + " " + tok})
	case "apikey":
		key, val := authField(f, "key"), authField(f, "value")
		if key == "" {
			return
		}
		if strings.EqualFold(authField(f, "in"), "query") {
			*u = appendQuery(*u, []varstore.KV{{Key: key, Value: val}})
		} else if !hasHeader(b.Headers, key) {
			b.Headers = append(b.Headers, varstore.KV{Key: key, Value: val})
		}
	default:
		b.Warnings = append(b.Warnings, "auth type "+a.Type+" is not supported yet; request sent without it")
		return
	}
	b.AuthApplied = true
	b.Applied = append(b.Applied, "auth:"+a.Type)
}
