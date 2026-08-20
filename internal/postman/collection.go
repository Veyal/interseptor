// Package postman parses Postman Collection v2 JSON into editable HTTP
// requests. It deliberately stops at request preparation: Interseptor's
// Repeater, session handling, Intruder, and evidence store remain the runtime.
package postman

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var variablePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// Result is the safe-to-edit request set produced by a collection import.
// Requests are not sent or persisted as flows until the operator chooses to
// send them from Repeater.
type Result struct {
	Name       string    `json:"name"`
	Requests   []Request `json:"requests"`
	Unresolved []string  `json:"unresolved"`
	Warnings   []string  `json:"warnings,omitempty"`
	Skipped    int       `json:"skipped,omitempty"`
}

// Request is the subset of a Postman item that maps cleanly to Repeater.
type Request struct {
	Name     string   `json:"name"`
	Folder   string   `json:"folder,omitempty"`
	Method   string   `json:"method"`
	URL      string   `json:"url"`
	Headers  string   `json:"headers,omitempty"`
	Body     string   `json:"body,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Parse parses a Postman Collection v2 JSON document without an environment.
func Parse(collection []byte) (Result, error) {
	return ParseWithEnvironment(collection, nil)
}

// ParseWithEnvironment parses a collection and an optional exported Postman
// environment. Environment values override collection values, matching
// Postman's documented variable precedence for these two scopes.
func ParseWithEnvironment(collection, environment []byte) (Result, error) {
	var doc collectionDocument
	if err := json.Unmarshal(collection, &doc); err != nil {
		return Result{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if doc.Info.Name == "" || doc.Item == nil {
		return Result{}, errors.New("not a Postman collection: expected info and item")
	}
	if strings.Contains(strings.ToLower(doc.Info.Schema), "/v3") {
		return Result{}, errors.New("Postman Collection v3 YAML is not supported; export Collection v2.1 JSON")
	}

	values := variablesToMap(doc.Variable, func(v postmanVariable) bool { return !v.Disabled })
	if len(bytes.TrimSpace(environment)) > 0 {
		var env environmentDocument
		if err := json.Unmarshal(environment, &env); err != nil {
			return Result{}, fmt.Errorf("invalid Postman environment JSON: %w", err)
		}
		for key, value := range variablesToMap(env.Values, func(v postmanVariable) bool { return v.Enabled == nil || *v.Enabled }) {
			values[key] = value
		}
	}

	p := parser{unresolved: make(map[string]struct{})}
	requests := make([]Request, 0)
	for _, item := range doc.Item {
		p.walk(item, "", cloneMap(values), parseAuth(doc.Auth), &requests)
	}
	if len(requests) == 0 {
		return Result{}, errors.New("Postman collection contains no importable HTTP requests")
	}

	result := Result{Name: doc.Info.Name, Requests: requests, Skipped: p.skipped}
	for key := range p.unresolved {
		result.Unresolved = append(result.Unresolved, key)
	}
	sort.Strings(result.Unresolved)
	result.Warnings = uniqueStrings(p.warnings)
	return result, nil
}

type collectionDocument struct {
	Info     collectionInfo    `json:"info"`
	Item     []collectionItem  `json:"item"`
	Variable []postmanVariable `json:"variable"`
	Auth     json.RawMessage   `json:"auth"`
}

type collectionInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type environmentDocument struct {
	Values []postmanVariable `json:"values"`
}

type postmanVariable struct {
	Key          string          `json:"key"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Value        json.RawMessage `json:"value"`
	InitialValue json.RawMessage `json:"initialValue"`
	Disabled     bool            `json:"disabled"`
	Enabled      *bool           `json:"enabled"`
}

type collectionItem struct {
	Name     string            `json:"name"`
	Request  json.RawMessage   `json:"request"`
	Item     []collectionItem  `json:"item"`
	Variable []postmanVariable `json:"variable"`
	Auth     json.RawMessage   `json:"auth"`
}

type requestDocument struct {
	URL    json.RawMessage `json:"url"`
	Method string          `json:"method"`
	Header json.RawMessage `json:"header"`
	Body   json.RawMessage `json:"body"`
	Auth   json.RawMessage `json:"auth"`
}

type bodyDocument struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []bodyParameter `json:"urlencoded"`
	FormData   []formParameter `json:"formdata"`
	GraphQL    json.RawMessage `json:"graphql"`
	Disabled   bool            `json:"disabled"`
}

type bodyParameter struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Disabled bool            `json:"disabled"`
}

type formParameter struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Type        string          `json:"type"`
	Src         json.RawMessage `json:"src"`
	ContentType string          `json:"contentType"`
	Disabled    bool            `json:"disabled"`
}

type headerDocument struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type queryDocument struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type urlDocument struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     json.RawMessage `json:"host"`
	Path     json.RawMessage `json:"path"`
	Port     string          `json:"port"`
	Query    []queryDocument `json:"query"`
}

type authDocument struct {
	Type   string      `json:"type"`
	Bearer []authEntry `json:"bearer"`
	Basic  []authEntry `json:"basic"`
	APIKey []authEntry `json:"apikey"`
	OAuth2 []authEntry `json:"oauth2"`
}

type authEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type authDefinition struct {
	Type   string
	Values []authEntry
}

type parser struct {
	unresolved map[string]struct{}
	warnings   []string
	skipped    int
}

func (p *parser) walk(item collectionItem, folder string, values map[string]string, inheritedAuth *authDefinition, out *[]Request) {
	for key, value := range variablesToMap(item.Variable, func(v postmanVariable) bool { return !v.Disabled }) {
		values[key] = value
	}
	auth := inheritedAuth
	if len(item.Auth) > 0 {
		auth = parseAuth(item.Auth)
	}

	if len(item.Request) > 0 && string(bytes.TrimSpace(item.Request)) != "null" {
		request, ok := p.parseRequest(item, folder, values, auth)
		if ok {
			*out = append(*out, request)
		}
		return
	}

	nextFolder := folder
	if item.Name != "" {
		if nextFolder != "" {
			nextFolder += " / "
		}
		nextFolder += item.Name
	}
	for _, child := range item.Item {
		p.walk(child, nextFolder, cloneMap(values), auth, out)
	}
}

func (p *parser) parseRequest(item collectionItem, folder string, values map[string]string, inheritedAuth *authDefinition) (Request, bool) {
	var rawURL string
	var doc requestDocument
	trimmed := bytes.TrimSpace(item.Request)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &rawURL); err != nil {
			p.skipped++
			p.warnings = append(p.warnings, fmt.Sprintf("%s: invalid request URL", item.Name))
			return Request{}, false
		}
		doc.Method = "GET"
		doc.URL = json.RawMessage(strconv.Quote(rawURL))
	} else if err := json.Unmarshal(trimmed, &doc); err != nil {
		p.skipped++
		p.warnings = append(p.warnings, fmt.Sprintf("%s: invalid request object", item.Name))
		return Request{}, false
	}

	u, err := parseRequestURL(doc.URL, values, p)
	if err != nil || u == "" {
		p.skipped++
		p.warnings = append(p.warnings, fmt.Sprintf("%s: missing request URL", item.Name))
		return Request{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(doc.Method))
	if method == "" {
		method = "GET"
	}

	lines := parseHeaders(doc.Header, values, p)
	body, contentType, bodyWarnings := parseBody(doc.Body, values, p)
	warnings := append([]string(nil), bodyWarnings...)
	if contentType != "" && !hasHeader(lines, "Content-Type") {
		lines = append(lines, "Content-Type: "+contentType)
	}
	effectiveAuth := inheritedAuth
	if len(doc.Auth) > 0 {
		effectiveAuth = parseAuth(doc.Auth)
	}
	u, authLines, authWarnings := applyAuth(u, lines, effectiveAuth, values, p)
	lines = append(lines, authLines...)
	warnings = append(warnings, authWarnings...)
	for _, warning := range warnings {
		p.warnings = append(p.warnings, item.Name+": "+warning)
	}

	return Request{
		Name:     item.Name,
		Folder:   folder,
		Method:   method,
		URL:      u,
		Headers:  strings.Join(lines, "\n"),
		Body:     body,
		Warnings: uniqueStrings(warnings),
	}, true
}

func parseRequestURL(raw json.RawMessage, values map[string]string, p *parser) (string, error) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return p.resolve(s, values), nil
	}
	var doc urlDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	if doc.Raw != "" {
		return p.resolve(doc.Raw, values), nil
	}

	scheme := p.resolve(doc.Protocol, values)
	host := p.resolve(rawStringOrJoined(doc.Host), values)
	if host == "" {
		return "", errors.New("missing URL host")
	}
	if scheme == "" {
		scheme = "https"
	}
	port := p.resolve(doc.Port, values)
	authority := host
	if port != "" {
		authority += ":" + port
	}
	path := rawPath(doc.Path, p, values)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out := scheme + "://" + authority + path
	query := make([]string, 0, len(doc.Query))
	for _, q := range doc.Query {
		if q.Disabled || q.Key == "" {
			continue
		}
		key := p.resolve(q.Key, values)
		value := p.resolve(q.Value, values)
		query = append(query, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	if len(query) > 0 {
		out += "?" + strings.Join(query, "&")
	}
	return out, nil
}

func rawStringOrJoined(raw json.RawMessage) string {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	if json.Unmarshal(raw, &parts) == nil {
		return strings.Join(parts, ".")
	}
	return ""
}

func rawPath(raw json.RawMessage, p *parser, values map[string]string) string {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return p.resolve(s, values)
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	valuesOut := make([]string, 0, len(parts))
	for _, part := range parts {
		var text string
		if json.Unmarshal(part, &text) == nil {
			valuesOut = append(valuesOut, p.resolve(text, values))
			continue
		}
		var variable struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(part, &variable) == nil {
			valuesOut = append(valuesOut, p.resolve(variable.Value, values))
		}
	}
	return strings.Join(valuesOut, "/")
}

func parseHeaders(raw json.RawMessage, values map[string]string, p *parser) []string {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				out = append(out, p.resolve(line, values))
			}
		}
		return out
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		var header headerDocument
		if json.Unmarshal(entry, &header) == nil && header.Key != "" {
			if !header.Disabled {
				out = append(out, p.resolve(header.Key, values)+": "+p.resolve(header.Value, values))
			}
			continue
		}
		var line string
		if json.Unmarshal(entry, &line) == nil && strings.TrimSpace(line) != "" {
			out = append(out, p.resolve(line, values))
		}
	}
	return out
}

func parseBody(raw json.RawMessage, values map[string]string, p *parser) (string, string, []string) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", "", nil
	}
	var body bodyDocument
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", "", []string{"request body could not be imported"}
	}
	if body.Disabled {
		return "", "", nil
	}
	switch strings.ToLower(body.Mode) {
	case "", "raw":
		return p.resolve(body.Raw, values), "", nil
	case "urlencoded":
		parts := make([]string, 0, len(body.URLEncoded))
		for _, field := range body.URLEncoded {
			if field.Disabled || field.Key == "" {
				continue
			}
			parts = append(parts, url.QueryEscape(p.resolve(field.Key, values))+"="+url.QueryEscape(resolveJSONValue(field.Value, p, values)))
		}
		return strings.Join(parts, "&"), "application/x-www-form-urlencoded", nil
	case "graphql":
		return graphqlBody(body.GraphQL, p, values)
	case "formdata":
		return multipartBody(body.FormData, p, values)
	case "file":
		return "", "", []string{"file bodies need a local file path and were not imported"}
	default:
		return "", "", []string{"body mode " + body.Mode + " is not supported"}
	}
}

func graphqlBody(raw json.RawMessage, p *parser, values map[string]string) (string, string, []string) {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", "application/json", []string{"GraphQL body is empty"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", "application/json", []string{"GraphQL body could not be imported"}
	}
	out := make(map[string]any, len(fields))
	for key, field := range fields {
		var value any
		if err := json.Unmarshal(field, &value); err != nil {
			return "", "application/json", []string{"GraphQL body could not be imported"}
		}
		value = resolveJSONStructure(value, p, values)
		if key == "variables" {
			if text, ok := value.(string); ok {
				var decoded any
				if json.Unmarshal([]byte(text), &decoded) == nil {
					value = resolveJSONStructure(decoded, p, values)
				}
			}
		}
		out[key] = value
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", "application/json", []string{"GraphQL body could not be encoded"}
	}
	return string(b), "application/json", nil
}

func resolveJSONStructure(value any, p *parser, values map[string]string) any {
	switch value := value.(type) {
	case string:
		return p.resolve(value, values)
	case []any:
		for i := range value {
			value[i] = resolveJSONStructure(value[i], p, values)
		}
	case map[string]any:
		for key := range value {
			value[key] = resolveJSONStructure(value[key], p, values)
		}
	}
	return value
}

func multipartBody(fields []formParameter, p *parser, values map[string]string) (string, string, []string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	warnings := make([]string, 0)
	for _, field := range fields {
		if field.Disabled || field.Key == "" {
			continue
		}
		key := p.resolve(field.Key, values)
		if strings.EqualFold(field.Type, "file") || len(field.Src) > 0 && string(bytes.TrimSpace(field.Src)) != "null" {
			warnings = append(warnings, "file form field "+key+" was not imported")
			continue
		}
		value := resolveJSONValue(field.Value, p, values)
		var part io.Writer
		var err error
		if field.ContentType == "" {
			part, err = w.CreateFormField(key)
		} else {
			contentType := p.resolve(field.ContentType, values)
			if _, _, parseErr := mime.ParseMediaType(contentType); parseErr != nil {
				warnings = append(warnings, "form field "+key+" has invalid content type and was imported without it")
				part, err = w.CreateFormField(key)
			} else {
				header := make(textproto.MIMEHeader)
				header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, key))
				header.Set("Content-Type", contentType)
				part, err = w.CreatePart(header)
			}
		}
		if err != nil {
			warnings = append(warnings, "form field "+key+" was not imported")
			continue
		}
		_, _ = part.Write([]byte(value))
	}
	_ = w.Close()
	if buf.Len() == 0 {
		return "", "", warnings
	}
	return buf.String(), w.FormDataContentType(), warnings
}

func resolveJSONValue(raw json.RawMessage, p *parser, values map[string]string) string {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return p.resolve(s, values)
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return p.resolve(fmt.Sprint(value), values)
	}
	return ""
}

func parseAuth(raw json.RawMessage) *authDefinition {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var doc authDocument
	if json.Unmarshal(raw, &doc) != nil || doc.Type == "" {
		return nil
	}
	var values []authEntry
	switch strings.ToLower(doc.Type) {
	case "bearer":
		values = doc.Bearer
	case "basic":
		values = doc.Basic
	case "apikey":
		values = doc.APIKey
	case "oauth2":
		values = doc.OAuth2
	}
	return &authDefinition{Type: strings.ToLower(doc.Type), Values: values}
}

func applyAuth(rawURL string, headers []string, auth *authDefinition, values map[string]string, p *parser) (string, []string, []string) {
	if auth == nil || auth.Type == "noauth" || hasHeader(headers, "Authorization") {
		return rawURL, nil, nil
	}
	value := func(keys ...string) string {
		for _, key := range keys {
			for _, entry := range auth.Values {
				if strings.EqualFold(entry.Key, key) {
					return p.resolve(resolveJSONValue(entry.Value, p, values), values)
				}
			}
		}
		return ""
	}
	switch auth.Type {
	case "bearer":
		if token := value("token", "value"); token != "" {
			return rawURL, []string{"Authorization: Bearer " + token}, nil
		}
		return rawURL, nil, []string{"bearer auth has no token value"}
	case "basic":
		user, password := value("username"), value("password")
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		return rawURL, []string{"Authorization: Basic " + encoded}, nil
	case "apikey":
		key, secret, in := value("key"), value("value"), strings.ToLower(value("in"))
		if key == "" || secret == "" {
			return rawURL, nil, []string{"API key auth is missing a key or value"}
		}
		if in == "query" {
			separator := "?"
			if strings.Contains(rawURL, "?") {
				separator = "&"
			}
			return rawURL + separator + url.QueryEscape(key) + "=" + url.QueryEscape(secret), nil, nil
		}
		return rawURL, []string{key + ": " + secret}, nil
	case "oauth2":
		if token := value("accessToken", "token", "value"); token != "" {
			placement := strings.ToLower(strings.TrimSpace(value("addTokenTo")))
			switch placement {
			case "", "header":
				prefix := strings.TrimSpace(value("headerPrefix"))
				if prefix == "" {
					prefix = "Bearer"
				}
				return rawURL, []string{"Authorization: " + prefix + " " + token}, nil
			case "queryparams", "queryparam", "query", "url", "urlquery":
				return appendQueryParameter(rawURL, "access_token", token), nil, nil
			default:
				return rawURL, nil, []string{"OAuth 2 auth token placement " + placement + " is not supported"}
			}
		}
		return rawURL, nil, []string{"OAuth 2 auth has no access token value"}
	default:
		return rawURL, nil, []string{auth.Type + " auth was not converted to a Repeater header"}
	}
}

func appendQueryParameter(rawURL, key, value string) string {
	parameter := url.QueryEscape(key) + "=" + url.QueryEscape(value)
	fragment := ""
	if index := strings.IndexByte(rawURL, '#'); index >= 0 {
		fragment = rawURL[index:]
		rawURL = rawURL[:index]
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
		if strings.HasSuffix(rawURL, "?") || strings.HasSuffix(rawURL, "&") {
			separator = ""
		}
	}
	return rawURL + separator + parameter + fragment
}

func (p *parser) resolve(text string, values map[string]string) string {
	for i := 0; i < 8; i++ {
		changed := false
		text = variablePattern.ReplaceAllStringFunc(text, func(match string) string {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}"))
			if value, ok := values[name]; ok {
				changed = true
				return value
			}
			p.unresolved[name] = struct{}{}
			return match
		})
		if !changed {
			break
		}
	}
	return text
}

func variablesToMap(items []postmanVariable, enabled func(postmanVariable) bool) map[string]string {
	values := make(map[string]string, len(items))
	for _, item := range items {
		if !enabled(item) {
			continue
		}
		key := item.Key
		if key == "" {
			key = item.ID
		}
		if key == "" {
			key = item.Name
		}
		if key == "" {
			continue
		}
		raw := item.Value
		if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			raw = item.InitialValue
		}
		if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var anyValue any
			if json.Unmarshal(raw, &anyValue) == nil {
				value = fmt.Sprint(anyValue)
			}
		}
		values[key] = value
	}
	return values
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func hasHeader(lines []string, name string) bool {
	for _, line := range lines {
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), name) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
