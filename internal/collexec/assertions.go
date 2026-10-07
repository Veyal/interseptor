package collexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// assertionJSON is one declarative assertion (item assertions_json entry).
//
//	type: status | header | body | jsonpath | time
//	op:   eq ne in lt lte gt gte contains notcontains exists notexists regex
//	name: optional display name; header: header name; path: JSONPath ($.a.b[0])
type assertionJSON struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Op       string          `json:"op"`
	Header   string          `json:"header"`
	Path     string          `json:"path"`
	Value    json.RawMessage `json:"value"`
	Severity string          `json:"severity"`
	Disabled bool            `json:"disabled"`
}

func (a assertionJSON) valueString() string {
	var s string
	if json.Unmarshal(a.Value, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(a.Value))
}

func (a assertionJSON) display() string {
	if a.Name != "" {
		return a.Name
	}
	switch a.Type {
	case "header":
		return fmt.Sprintf("header %s %s %s", a.Header, a.Op, a.valueString())
	case "jsonpath":
		return fmt.Sprintf("%s %s %s", a.Path, a.Op, a.valueString())
	}
	return fmt.Sprintf("%s %s %s", a.Type, a.Op, a.valueString())
}

// evalAssertions runs every assertion against the response. Unknown types or
// JSONPath syntax produce "unsupported", never a pass.
func evalAssertions(raw json.RawMessage, resp *ResponseModel) []TestResult {
	var as []assertionJSON
	if len(raw) == 0 || json.Unmarshal(raw, &as) != nil {
		return nil
	}
	var out []TestResult
	for i, a := range as {
		if a.Disabled {
			continue
		}
		id := a.ID
		if id == "" {
			id = strconv.Itoa(i + 1)
		}
		t := TestResult{Name: a.display(), Severity: a.Severity, Owner: "request", Source: "assertion:" + id, Subject: a.Type}
		evalOne(&t, a, resp)
		out = append(out, t)
	}
	return out
}

func evalOne(t *TestResult, a assertionJSON, resp *ResponseModel) {
	switch a.Type {
	case "status":
		compareNum(t, a, float64(resp.Code))
	case "time":
		compareNum(t, a, float64(resp.TimeMs))
	case "header":
		vals := headerValues(resp, a.Header)
		switch a.Op {
		case "exists":
			setBool(t, len(vals) > 0, "present", fmt.Sprint(len(vals) > 0))
		case "notexists":
			setBool(t, len(vals) == 0, "absent", fmt.Sprint(len(vals) == 0))
		default:
			compareStr(t, a, strings.Join(vals, ", "))
		}
	case "body":
		compareStr(t, a, string(resp.Body))
	case "jsonpath":
		evalJSONPath(t, a, resp)
	default:
		t.Status, t.Message = TestUnsupported, "unknown assertion type "+a.Type
	}
}

func setBool(t *TestResult, ok bool, expected, actual string) {
	t.Expected, t.Actual = expected, actual
	if ok {
		t.Status = TestPass
	} else {
		t.Status = TestFail
	}
}

func headerValues(resp *ResponseModel, name string) []string {
	var out []string
	for _, h := range resp.Headers {
		if strings.EqualFold(h.Key, name) {
			out = append(out, h.Value)
		}
	}
	return out
}

func compareNum(t *TestResult, a assertionJSON, got float64) {
	t.Actual = strconv.FormatFloat(got, 'f', -1, 64)
	t.Expected = a.Op + " " + a.valueString()
	switch a.Op {
	case "in":
		var list []float64
		if json.Unmarshal(a.Value, &list) != nil {
			t.Status, t.Message = TestError, "value must be a number list"
			return
		}
		for _, n := range list {
			if n == got {
				t.Status = TestPass
				return
			}
		}
		t.Status = TestFail
		return
	}
	want, err := strconv.ParseFloat(a.valueString(), 64)
	if err != nil {
		t.Status, t.Message = TestError, "value must be a number"
		return
	}
	var ok bool
	switch a.Op {
	case "eq", "":
		ok = got == want
	case "ne":
		ok = got != want
	case "lt":
		ok = got < want
	case "lte":
		ok = got <= want
	case "gt":
		ok = got > want
	case "gte":
		ok = got >= want
	default:
		t.Status, t.Message = TestUnsupported, "operator "+a.Op+" for numbers"
		return
	}
	if ok {
		t.Status = TestPass
	} else {
		t.Status = TestFail
	}
}

func compareStr(t *TestResult, a assertionJSON, got string) {
	want := a.valueString()
	t.Expected = a.Op + " " + want
	t.Actual = clip(got, 200)
	var ok bool
	switch a.Op {
	case "eq", "":
		ok = got == want
	case "ne":
		ok = got != want
	case "contains":
		ok = strings.Contains(got, want)
	case "notcontains":
		ok = !strings.Contains(got, want)
	case "regex":
		re, err := regexp.Compile(want)
		if err != nil {
			t.Status, t.Message = TestError, "invalid regex"
			return
		}
		ok = re.MatchString(got)
	default:
		t.Status, t.Message = TestUnsupported, "operator "+a.Op+" for text"
		return
	}
	if ok {
		t.Status = TestPass
	} else {
		t.Status = TestFail
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func evalJSONPath(t *TestResult, a assertionJSON, resp *ResponseModel) {
	steps, err := parsePath(a.Path)
	if err != nil {
		t.Status, t.Message = TestUnsupported, err.Error()
		return
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(resp.Body))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Status, t.Message = TestFail, "response body is not JSON"
		return
	}
	cur, found := doc, true
	for _, s := range steps {
		if cur, found = s.apply(cur); !found {
			break
		}
	}
	switch a.Op {
	case "exists":
		setBool(t, found, "present", fmt.Sprint(found))
		return
	case "notexists":
		setBool(t, !found, "absent", fmt.Sprint(!found))
		return
	}
	if !found {
		t.Status, t.Expected, t.Actual, t.Message = TestFail, a.Op+" "+a.valueString(), "(missing)", "path not found"
		return
	}
	got := scalarString(cur)
	switch a.Op {
	case "lt", "lte", "gt", "gte", "in":
		n, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Status, t.Actual, t.Message = TestFail, clip(got, 200), "value at path is not a number"
			return
		}
		compareNum(t, a, n)
	default:
		compareStr(t, a, got)
	}
}

func scalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

type pathStep struct {
	key   string
	index int
	isIdx bool
}

func (s pathStep) apply(v any) (any, bool) {
	if s.isIdx {
		arr, ok := v.([]any)
		if !ok || s.index < 0 || s.index >= len(arr) {
			return nil, false
		}
		return arr[s.index], true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	x, ok := m[s.key]
	return x, ok
}

// parsePath supports the plain subset: $.a.b[0]['c d']. Wildcards, filters,
// slices and recursive descent are reported as unsupported.
func parsePath(p string) ([]pathStep, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("JSONPath must start with $")
	}
	var steps []pathStep
	i := 1
	for i < len(p) {
		switch p[i] {
		case '.':
			if strings.HasPrefix(p[i:], "..") {
				return nil, fmt.Errorf("JSONPath recursive descent is not supported")
			}
			j := i + 1
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			key := p[i+1 : j]
			if key == "" || key == "*" {
				return nil, fmt.Errorf("JSONPath wildcard is not supported")
			}
			steps = append(steps, pathStep{key: key})
			i = j
		case '[':
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("JSONPath: unterminated [")
			}
			inner := p[i+1 : i+j]
			i += j + 1
			if n, err := strconv.Atoi(inner); err == nil {
				steps = append(steps, pathStep{index: n, isIdx: true})
			} else if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
				steps = append(steps, pathStep{key: inner[1 : len(inner)-1]})
			} else {
				return nil, fmt.Errorf("JSONPath expression [%s] is not supported", inner)
			}
		default:
			return nil, fmt.Errorf("JSONPath: unexpected %q", p[i])
		}
	}
	return steps, nil
}
