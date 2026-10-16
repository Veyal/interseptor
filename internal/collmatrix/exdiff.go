package collmatrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Bounds of a saved-example diff.
const (
	DefaultMaxBody = 512 << 10
	MaxJSONChanges = 200
	maxJSONDepth   = 64
	maxClip        = 160
)

var errNotFound = errors.New("collmatrix: not found")

// volatileHeaders never count as a difference: they change on every response.
var volatileHeaders = []string{
	"date", "set-cookie", "x-request-id", "x-correlation-id", "x-trace-id", "x-amzn-trace-id", "cf-ray",
	"server-timing", "age", "etag", "expires", "last-modified", "content-length", "connection", "keep-alive",
	"transfer-encoding", "report-to", "nel", "alt-svc", "vary",
}

// Example is one saved response example of a request (Postman response[]).
type Example struct {
	Name    string   `json:"name"`
	Code    int      `json:"code"`
	Status  string   `json:"status,omitempty"`
	Headers []Header `json:"headers,omitempty"`
	Body    string   `json:"body,omitempty"`
}

// ParseExamples reads an item's examples column. Headers may be Postman's
// [{key,value}] list or a {name: value} object; code may be a number or text.
func ParseExamples(raw json.RawMessage) []Example {
	var arr []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	var out []Example
	for _, m := range arr {
		ex := Example{}
		_ = json.Unmarshal(m["name"], &ex.Name)
		_ = json.Unmarshal(m["status"], &ex.Status)
		_ = json.Unmarshal(m["body"], &ex.Body)
		if err := json.Unmarshal(m["code"], &ex.Code); err != nil {
			var s string
			if json.Unmarshal(m["code"], &s) == nil {
				ex.Code, _ = strconv.Atoi(s)
			}
		}
		for _, k := range []string{"header", "headers"} {
			if h := m[k]; len(h) > 0 {
				ex.Headers = parseHeaderJSON(h)
				break
			}
		}
		out = append(out, ex)
	}
	return out
}

func parseHeaderJSON(raw json.RawMessage) []Header {
	var list []struct {
		Key   string `json:"key"`
		Name  string `json:"name"`
		Value any    `json:"value"`
	}
	if json.Unmarshal(raw, &list) == nil {
		var out []Header
		for _, h := range list {
			n := h.Key
			if n == "" {
				n = h.Name
			}
			if n != "" {
				out = append(out, Header{Name: n, Value: fmt.Sprint(orEmpty(h.Value))})
			}
		}
		return out
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []Header
		for _, k := range keys {
			out = append(out, Header{Name: k, Value: fmt.Sprint(orEmpty(obj[k]))})
		}
		return out
	}
	return nil
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// Actual is the response compared with an example.
type Actual struct {
	Status  int
	Headers []Header
	Body    string
}

// DiffOptions tune a diff.
type DiffOptions struct {
	// IgnorePaths are JSON paths whose differences are ignored: "$.items[*].id",
	// "meta.took". "*" matches any key, "[*]" any array index; a path also
	// ignores everything below it.
	IgnorePaths []string `json:"ignorePaths,omitempty"`
	// IgnoreHeaders adds to the built-in volatile header list.
	IgnoreHeaders []string `json:"ignoreHeaders,omitempty"`
	MaxBody       int      `json:"maxBody,omitempty"`
}

// StatusDiff compares status codes.
type StatusDiff struct {
	Expected int  `json:"expected"`
	Actual   int  `json:"actual"`
	Changed  bool `json:"changed"`
}

// HeaderChange is one header difference.
type HeaderChange struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // added | removed | changed
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// JSONChange is one structural body difference.
type JSONChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"` // added | removed | changed | type
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// ExampleDiff is the result. ExpectedBody and ActualBody are normalized (JSON
// is key-sorted and indented) so a DiffView renders a stable line diff.
type ExampleDiff struct {
	Example       string         `json:"example"`
	Equal         bool           `json:"equal"`
	Status        StatusDiff     `json:"status"`
	Headers       []HeaderChange `json:"headers,omitempty"`
	BodyFormat    string         `json:"bodyFormat"` // json | text | empty
	BodyChanged   bool           `json:"bodyChanged"`
	JSON          []JSONChange   `json:"json,omitempty"`
	ChangesCapped bool           `json:"changesCapped,omitempty"`
	Truncated     bool           `json:"truncated,omitempty"`
	ExpectedBody  string         `json:"expectedBody,omitempty"`
	ActualBody    string         `json:"actualBody,omitempty"`
}

// DiffExample compares a saved example with an actual response.
func DiffExample(ex Example, act Actual, o DiffOptions) ExampleDiff {
	max := o.MaxBody
	if max <= 0 {
		max = DefaultMaxBody
	}
	d := ExampleDiff{Example: ex.Name, Status: StatusDiff{Expected: ex.Code, Actual: act.Status}}
	d.Status.Changed = ex.Code != 0 && act.Status != 0 && ex.Code != act.Status
	d.Headers = diffHeaders(ex.Headers, act.Headers, o.IgnoreHeaders)

	eb, ab := ex.Body, act.Body
	if len(eb) > max {
		eb, d.Truncated = eb[:max], true
	}
	if len(ab) > max {
		ab, d.Truncated = ab[:max], true
	}
	switch {
	case strings.TrimSpace(eb) == "" && strings.TrimSpace(ab) == "":
		d.BodyFormat = "empty"
	default:
		ej, eok := parseJSONValue(eb)
		aj, aok := parseJSONValue(ab)
		if eok && aok && !d.Truncated {
			d.BodyFormat = "json"
			d.ExpectedBody, d.ActualBody = prettyJSON(ej), prettyJSON(aj)
			var pats []pathPattern
			for _, p := range o.IgnorePaths {
				pats = append(pats, parsePattern(p))
			}
			c := &jsonCmp{ignore: pats}
			c.diff(nil, ej, aj, 0)
			d.JSON, d.ChangesCapped = c.out, c.capped
			d.BodyChanged = len(d.JSON) > 0
		} else {
			d.BodyFormat = "text"
			d.ExpectedBody, d.ActualBody = eb, ab
			d.BodyChanged = eb != ab
		}
	}
	d.Equal = !d.Status.Changed && len(d.Headers) == 0 && !d.BodyChanged
	return d
}

func parseJSONValue(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return nil, false
	}
	return v, true
}

func prettyJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // map keys are emitted sorted
	return strings.TrimRight(buf.String(), "\n")
}

func diffHeaders(exp, act []Header, extraIgnore []string) []HeaderChange {
	skip := append(append([]string(nil), volatileHeaders...), extraIgnore...)
	fold := func(hs []Header) (map[string]string, []string) {
		m := map[string]string{}
		var order []string
		for _, h := range hs {
			k := strings.ToLower(strings.TrimSpace(h.Name))
			if k == "" || hasHeaderName(skip, k) {
				continue
			}
			if _, ok := m[k]; ok {
				m[k] += ", " + h.Value
			} else {
				m[k] = h.Value
				order = append(order, h.Name)
			}
		}
		return m, order
	}
	em, eorder := fold(exp)
	am, aorder := fold(act)
	var out []HeaderChange
	for _, n := range eorder {
		k := strings.ToLower(n)
		av, ok := am[k]
		switch {
		case !ok:
			out = append(out, HeaderChange{Name: n, Kind: "removed", Expected: em[k]})
		case normHeaderValue(av) != normHeaderValue(em[k]):
			out = append(out, HeaderChange{Name: n, Kind: "changed", Expected: em[k], Actual: av})
		}
	}
	for _, n := range aorder {
		if _, ok := em[strings.ToLower(n)]; !ok {
			out = append(out, HeaderChange{Name: n, Kind: "added", Actual: am[strings.ToLower(n)]})
		}
	}
	return out
}

func normHeaderValue(v string) string { return strings.Join(strings.Fields(strings.ToLower(v)), " ") }

// ---- JSON structural diff ---------------------------------------------------

type pathPattern []string

// parsePattern turns "$.items[*].id" into ["items","[*]","id"].
func parsePattern(p string) pathPattern {
	p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "$"))
	var seg pathPattern
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			seg = append(seg, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '.':
			flush()
		case '[':
			flush()
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				cur.WriteString(p[i:])
				i = len(p)
				break
			}
			seg = append(seg, p[i:i+j+1])
			i += j
		default:
			cur.WriteByte(p[i])
		}
	}
	flush()
	return seg
}

func (pp pathPattern) covers(path []string) bool {
	if len(pp) == 0 || len(pp) > len(path) {
		return false
	}
	for i, s := range pp {
		switch {
		case s == "*":
		case s == "[*]":
			if !strings.HasPrefix(path[i], "[") {
				return false
			}
		case s != path[i]:
			return false
		}
	}
	return true
}

type jsonCmp struct {
	ignore []pathPattern
	out    []JSONChange
	capped bool
}

func (c *jsonCmp) ignored(path []string) bool {
	for _, p := range c.ignore {
		if p.covers(path) {
			return true
		}
	}
	return false
}

func renderPath(path []string) string {
	var sb strings.Builder
	for i, s := range path {
		if !strings.HasPrefix(s, "[") && i > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(s)
	}
	return sb.String()
}

func clip(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > maxClip {
		s = s[:maxClip] + "..."
	}
	return s
}

func (c *jsonCmp) add(path []string, kind string, e, a any, hasE, hasA bool) {
	if len(c.out) >= MaxJSONChanges {
		c.capped = true
		return
	}
	ch := JSONChange{Path: renderPath(path), Kind: kind}
	if hasE {
		ch.Expected = clip(e)
	}
	if hasA {
		ch.Actual = clip(a)
	}
	c.out = append(c.out, ch)
}

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "null"
}

func (c *jsonCmp) diff(path []string, e, a any, depth int) {
	if c.capped || c.ignored(path) {
		return
	}
	if jsonKind(e) != jsonKind(a) {
		c.add(path, "type", e, a, true, true)
		return
	}
	if depth > maxJSONDepth {
		return
	}
	switch ev := e.(type) {
	case map[string]any:
		av := a.(map[string]any)
		keys := map[string]bool{}
		for k := range ev {
			keys[k] = true
		}
		for k := range av {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			p := append(append([]string(nil), path...), k)
			x, inE := ev[k]
			y, inA := av[k]
			switch {
			case inE && inA:
				c.diff(p, x, y, depth+1)
			case inE:
				if !c.ignored(p) {
					c.add(p, "removed", x, nil, true, false)
				}
			default:
				if !c.ignored(p) {
					c.add(p, "added", nil, y, false, true)
				}
			}
		}
	case []any:
		av := a.([]any)
		n := len(ev)
		if len(av) > n {
			n = len(av)
		}
		for i := 0; i < n; i++ {
			p := append(append([]string(nil), path...), "["+strconv.Itoa(i)+"]")
			switch {
			case i < len(ev) && i < len(av):
				c.diff(p, ev[i], av[i], depth+1)
			case i < len(ev):
				if !c.ignored(p) {
					c.add(p, "removed", ev[i], nil, true, false)
				}
			default:
				if !c.ignored(p) {
					c.add(p, "added", nil, av[i], false, true)
				}
			}
		}
	default:
		if clip(e) != clip(a) || fmt.Sprint(e) != fmt.Sprint(a) {
			c.add(path, "changed", e, a, true, true)
		}
	}
}

// ---- service ----------------------------------------------------------------

// DiffExample diffs one saved example of an item (selected by name, or by
// 1-based index when name is a number) against a captured response flow.
// Everything returned passes through the scrubber.
func (s *Service) DiffExample(ctx context.Context, collectionUID, itemUID, example string, flowID int64, o DiffOptions) (*ExampleDiff, error) {
	if s.d.Bodies == nil {
		return nil, errors.New("collmatrix: no flow body reader configured")
	}
	_, items, err := s.d.Backend.Load(collectionUID)
	if err != nil {
		return nil, err
	}
	var exs []Example
	found := false
	for _, it := range items {
		if it.UID == itemUID {
			exs, found = ParseExamples(it.Examples), true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("collmatrix: item %q not found", itemUID)
	}
	ex, ok := pickExample(exs, example)
	if !ok {
		return nil, fmt.Errorf("collmatrix: example %q not found", example)
	}
	fb, err := s.d.Bodies.FlowBody(flowID, int64(DefaultMaxBody))
	if err != nil {
		return nil, fmt.Errorf("collmatrix: flow %d: %w", flowID, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := DiffExample(ex, Actual{Status: fb.Status, Headers: fb.Headers, Body: string(fb.Body)}, o)
	s.scrubDiff(&d)
	return &d, nil
}

func pickExample(exs []Example, sel string) (Example, bool) {
	sel = strings.TrimSpace(sel)
	for _, e := range exs {
		if e.Name == sel {
			return e, true
		}
	}
	if n, err := strconv.Atoi(sel); err == nil && n >= 1 && n <= len(exs) {
		return exs[n-1], true
	}
	if sel == "" && len(exs) > 0 {
		return exs[0], true
	}
	return Example{}, false
}

func (s *Service) scrubDiff(d *ExampleDiff) {
	d.ExpectedBody, d.ActualBody = s.scrub(d.ExpectedBody), s.scrub(d.ActualBody)
	for i := range d.Headers {
		d.Headers[i].Expected, d.Headers[i].Actual = s.scrub(d.Headers[i].Expected), s.scrub(d.Headers[i].Actual)
	}
	for i := range d.JSON {
		d.JSON[i].Expected, d.JSON[i].Actual = s.scrub(d.JSON[i].Expected), s.scrub(d.JSON[i].Actual)
	}
}
