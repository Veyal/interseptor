package mcp

import (
	"fmt"
	"strings"
	"time"
)

// splitCSV splits a comma-separated argument, trimming and dropping empties.
func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// argList reads a list argument given either as a comma- or newline-separated
// string or as a JSON array (whose elements may themselves be separated
// lists), trimming entries and dropping empties.
func argList(a map[string]any, key string) []string {
	split := func(raw string) []string {
		var out []string
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
			if v := strings.TrimSpace(part); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	var out []string
	switch x := a[key].(type) {
	case nil:
	case []any:
		for _, el := range x {
			if el != nil {
				out = append(out, split(argStr(map[string]any{"v": el}, "v"))...)
			}
		}
	case []string:
		for _, el := range x {
			out = append(out, split(el)...)
		}
	default:
		out = split(argStr(a, key))
	}
	return out
}

// flowSideArg maps the side names agents send to the ones the control API
// reads. The raw endpoint treats anything but "res" as the request, so an
// unnormalized "response" returned the wrong message. Unknown values pass
// through unchanged.
func flowSideArg(side string) string {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "", "both", "all":
		return "both"
	case "req", "request":
		return "req"
	case "res", "resp", "response":
		return "res"
	}
	return side
}

// humanWindow renders the time left before an undo deadline.
func humanWindow(expiresMS int64) string {
	d := time.Until(time.UnixMilli(expiresMS))
	if d <= 0 {
		return "a moment"
	}
	if d > time.Minute {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}
