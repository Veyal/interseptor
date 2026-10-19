package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// activityResultMax caps the Activity result snippet (the feed shows one line).
const activityResultMax = 160

var (
	// sectionMarker matches decoration lines such as "=== REQUEST ===" or "---".
	sectionMarker = regexp.MustCompile(`^(=+[^=]*=+|-{3,})$`)
	// uiFlowLink is the "UI: <base>/#flow-N" line tools append to their output.
	uiFlowLink = regexp.MustCompile(`/#flow-(\d+)\b`)
)

// activityResult turns a tool's text (or error) into the Activity result: the
// first informative line, with a lone "Label:" joined to the line after it, and
// "flow #N" appended when the output links a flow, so the feed can open it.
func activityResult(text string) string {
	snippet := informativeLine(text)
	m := uiFlowLink.FindStringSubmatch(text)
	if m == nil || strings.Contains(strings.ToLower(snippet), "flow #"+m[1]) {
		return capRunes(snippet, activityResultMax)
	}
	link := "flow #" + m[1]
	if snippet == "" {
		return link
	}
	return fmt.Sprintf("%s · %s", capRunes(snippet, activityResultMax), link)
}

// informativeLine skips blank lines, section markers and the UI link line.
func informativeLine(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || sectionMarker.MatchString(l) || strings.HasPrefix(l, "UI: ") {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return ""
	}
	if strings.HasSuffix(lines[0], ":") && len(lines) > 1 {
		return lines[0] + " " + lines[1]
	}
	return lines[0]
}

// capRunes trims s to n runes, marking the cut with an ellipsis.
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// sentFlowSummary is the part of the send_request flow detail that the first
// line of its result (and so its Activity row) reports.
type sentFlowSummary struct {
	ID         int64  `json:"id"`
	Method     string `json:"method"`
	Scheme     string `json:"scheme"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
}

// line renders "Sent → flow #N · 200 · GET host/path · 12 ms", or "" when the
// response named no flow.
func (f sentFlowSummary) line() string {
	if f.ID == 0 {
		return ""
	}
	status := "no response"
	if f.Status != 0 {
		status = strconv.Itoa(f.Status)
	}
	host := f.Host
	if f.Port != 0 && !(f.Scheme == "https" && f.Port == 443) && !(f.Scheme == "http" && f.Port == 80) {
		host += ":" + strconv.Itoa(f.Port)
	}
	parts := []string{fmt.Sprintf("Sent → flow #%d", f.ID), status, strings.TrimSpace(f.Method + " " + host + f.Path)}
	if f.Error != "" {
		parts = append(parts, f.Error)
	} else if f.DurationMs > 0 {
		parts = append(parts, fmt.Sprintf("%d ms", f.DurationMs))
	}
	return strings.Join(parts, " · ")
}

// activityFailed reports whether a tool call that returned normally still did
// not succeed. send_request returns the recorded flow even when the connection
// failed (refused, timed out), so its Activity row must read as an error.
func activityFailed(tool, text string) bool {
	if tool != "send_request" {
		return false
	}
	i := strings.Index(text, "{")
	if i < 0 {
		return false
	}
	var f sentFlowSummary
	if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&f); err != nil {
		return false
	}
	return f.ID != 0 && (f.Error != "" || f.Status == 0)
}
