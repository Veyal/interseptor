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
