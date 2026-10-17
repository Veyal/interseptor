package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// validateArgs checks tool arguments against the tool's declared input schema
// before the tool runs: required fields, JSON types (including oneOf
// alternatives), string enums and numeric bounds. Every problem names the field
// and what was expected so an agent can correct the call in one retry instead
// of a tool silently defaulting (set_session {} once wiped the session) or
// failing deep in the stack with a misleading message.
//
// It is deliberately lenient where agents commonly send equivalent values:
// numeric strings satisfy integer/number, scalars satisfy string, and the
// strings "true"/"false" satisfy boolean (normalized in place so the tool's
// argBool reads them). Unknown keys are not rejected — several tools accept
// documented aliases (flowId for id, count for repeat).
func validateArgs(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	var problems []string
	for _, key := range requiredKeys(schema) {
		v, ok := args[key]
		if !ok || v == nil {
			v, ok = aliasValue(args, key)
		}
		if !ok || v == nil {
			problems = append(problems, key+" is required"+expectedSuffix(props[key]))
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" && schemaAllows(props[key], "string") && !schemaAllows(props[key], "object") {
			problems = append(problems, key+" is required (got an empty string)")
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		ps, ok := props[key].(map[string]any)
		if !ok || args[key] == nil {
			continue
		}
		if sv, isStr := args[key].(string); isStr && strings.TrimSpace(sv) == "" && !schemaAllows(ps, "string") && len(schemaTypes(ps)) > 0 {
			continue // "" for a non-string optional field reads as omitted
		}
		if msg := checkValue(key, ps, args); msg != "" {
			problems = append(problems, msg)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid arguments: %s", strings.Join(problems, "; "))
}

// argAliases are the alternate keys tools accept for a required argument
// (reqFlowID reads id or flowId; diff_flows reads a/id1/flowA).
var argAliases = map[string][]string{
	"id":     {"flowId"},
	"flowId": {"id"},
	"a":      {"id1", "flowA"},
	"b":      {"id2", "flowB"},
}

func aliasValue(args map[string]any, key string) (any, bool) {
	for _, alt := range argAliases[key] {
		if v, ok := args[alt]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func requiredKeys(schema map[string]any) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []any:
		out := make([]string, 0, len(r))
		for _, v := range r {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// schemaTypes lists the JSON types a property accepts: its own type, or every
// oneOf/anyOf branch type. Empty means untyped (anything goes).
func schemaTypes(ps any) []string {
	m, ok := ps.(map[string]any)
	if !ok {
		return nil
	}
	if t, ok := m["type"].(string); ok {
		return []string{t}
	}
	var out []string
	for _, k := range []string{"oneOf", "anyOf"} {
		alts, _ := m[k].([]any)
		for _, alt := range alts {
			out = append(out, schemaTypes(alt)...)
		}
	}
	return out
}

func schemaAllows(ps any, typ string) bool { return slices.Contains(schemaTypes(ps), typ) }

func expectedSuffix(ps any) string {
	m, _ := ps.(map[string]any)
	if enum := enumValues(m); len(enum) > 0 {
		return " (one of " + strings.Join(enum, " | ") + ")"
	}
	if ts := schemaTypes(ps); len(ts) > 0 {
		return " (" + strings.Join(ts, " or ") + ")"
	}
	return ""
}

func enumValues(m map[string]any) []string {
	switch e := m["enum"].(type) {
	case []string:
		return e
	case []any:
		out := make([]string, 0, len(e))
		for _, v := range e {
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	return nil
}

// checkValue validates one present argument and returns a problem, or "".
func checkValue(key string, ps map[string]any, args map[string]any) string {
	v := args[key]
	types := schemaTypes(ps)
	if len(types) > 0 {
		matched := ""
		for _, t := range types {
			if nv, ok := matchType(t, v); ok {
				matched = t
				args[key] = nv
				break
			}
		}
		if matched == "" {
			return fmt.Sprintf("%s must be %s (got %s %s)", key, strings.Join(articled(types), " or "), jsonKind(v), describeValue(key, v))
		}
		v = args[key]
	}
	if enum := enumValues(ps); len(enum) > 0 {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" && !containsFold(enum, s) {
			return fmt.Sprintf("%s must be one of %s (got %s)", key, strings.Join(enum, " | "), describeValue(key, v))
		}
	}
	if n, ok := numeric(v); ok {
		if lo, ok := numeric(ps["minimum"]); ok && n < lo {
			return fmt.Sprintf("%s must be >= %v (got %v)", key, lo, n)
		}
		if hi, ok := numeric(ps["maximum"]); ok && n > hi {
			return fmt.Sprintf("%s must be <= %v (got %v)", key, hi, n)
		}
	}
	return ""
}

// matchType reports whether v satisfies JSON type t, returning the (possibly
// normalized) value to store back.
func matchType(t string, v any) (any, bool) {
	switch t {
	case "string":
		switch v.(type) {
		case string, float64, float32, int, int64, json.Number, bool:
			return v, true
		}
		return v, false
	case "integer":
		n, ok := numeric(v)
		return v, ok && n == math.Trunc(n)
	case "number":
		_, ok := numeric(v)
		return v, ok
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, true
		case string:
			if b, err := strconv.ParseBool(strings.TrimSpace(x)); err == nil {
				return b, true
			}
		}
		return v, false
	case "object":
		_, ok := v.(map[string]any)
		return v, ok
	case "array":
		rv := reflect.ValueOf(v)
		return v, rv.Kind() == reflect.Slice
	}
	return v, true
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func jsonKind(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case nil:
		return "null"
	}
	if _, ok := numeric(v); ok {
		return "number"
	}
	if reflect.ValueOf(v).Kind() == reflect.Slice {
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

func articled(types []string) []string {
	out := make([]string, len(types))
	for i, t := range types {
		switch t {
		case "integer", "object", "array":
			out[i] = "an " + t
		default:
			out[i] = "a " + t
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	s = strings.TrimSpace(s)
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// pe declares a string parameter restricted to values (matched
// case-insensitively by validateArgs).
func pe(desc string, values ...string) map[string]any {
	m := map[string]any{"type": "string", "enum": values}
	if desc != "" {
		m["description"] = desc
	}
	return m
}

// pr declares an integer parameter with inclusive bounds.
func pr(desc string, lo, hi int) map[string]any {
	m := map[string]any{"type": "integer", "minimum": lo, "maximum": hi}
	if desc != "" {
		m["description"] = desc
	}
	return m
}

// errorOnlyResult reports whether a successful control-API reply is really a
// failure: a JSON object whose only field is a non-empty "error" string (for
// example /api/decode answers 200 {"error":"unknown op"} for the UI's inline
// display). Such results are surfaced to the agent as isError.
func errorOnlyResult(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") {
		return "", false
	}
	var m map[string]any
	if json.Unmarshal([]byte(t), &m) != nil || len(m) != 1 {
		return "", false
	}
	msg, ok := m["error"].(string)
	if !ok || strings.TrimSpace(msg) == "" {
		return "", false
	}
	return msg, true
}
