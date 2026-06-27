// Package httplines normalizes HTTP header input from APIs and MCP tools.
// Agents often pass headers as a JSON object; humans use "Key: Value" lines.
package httplines

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToMap parses raw "Key: Value" lines into a header map.
func ToMap(s string) map[string][]string {
	h := map[string][]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		h[k] = append(h[k], v)
	}
	return h
}

// ToLines renders a header map as "Key: Value" lines (sorted by key).
func ToLines(h map[string][]string) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// NormalizeJSON accepts JSON null, a "Key: Value\n…" string, or a {"Key":"Value"} object.
func NormalizeJSON(raw json.RawMessage) (map[string][]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return ToMap(s), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("headers must be \"Key: Value\" lines or a JSON object")
	}
	out := map[string][]string{}
	for k, v := range obj {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		switch t := v.(type) {
		case string:
			out[k] = append(out[k], t)
		case []any:
			for _, item := range t {
				out[k] = append(out[k], fmt.Sprint(item))
			}
		case nil:
		default:
			out[k] = append(out[k], fmt.Sprint(t))
		}
	}
	return out, nil
}

// NormalizeArg is the MCP/API helper: string lines, JSON object map, or empty.
func NormalizeArg(v any) (map[string][]string, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return ToMap(s), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return NormalizeJSON(b)
}
