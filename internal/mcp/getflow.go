package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const getFlowDescription = "Read a flow's request and/or response: the start line and headers always, plus a bounded body window. " +
	"Bodies larger than maxBytes are paged with offset (the reply names the next offset); grep=<regex> searches the whole (decoded) body and returns matching lines with context instead of a window. " +
	"Binary bodies are described (size, MIME, hash, hex head) instead of dumped."

func getFlowSchema() map[string]any {
	return obj(map[string]any{
		"id":       pt("integer"),
		"side":     pe("default both", "req", "res", "both", "request", "response"),
		"maxBytes": pr("body bytes per side (default 4000; 0 = headers only)", 0, 1000000),
		"offset":   map[string]any{"type": "integer", "minimum": 0, "description": "body byte offset to start the window at (default 0)"},
		"grep":     p("string", "Go regexp searched across the whole body, e.g. (?i)token|password"),
		"context":  pr("lines of context around each grep match (default 1)", 0, 10),
	}, "id")
}

// flowSlice mirrors the control API's GET /api/flows/{id}/slice reply.
type flowSlice struct {
	Head      string `json:"head"`
	Mime      string `json:"mime"`
	BodyBytes int64  `json:"bodyBytes"`
	BodyHash  string `json:"bodyHash"`
	Binary    bool   `json:"binary"`
	HexHead   string `json:"hexHead"`
	Offset    int    `json:"offset"`
	Text      string `json:"text"`
	Shown     int    `json:"shown"`
	More      bool   `json:"more"`
	Next      int    `json:"nextOffset"`
	Decoded   bool   `json:"decoded"`
	Capped    bool   `json:"decodeCap"`
	Grep      *struct {
		Pattern string `json:"pattern"`
		Count   int    `json:"count"`
		Matches []struct {
			Line   int    `json:"line"`
			Offset int    `json:"offset"`
			Text   string `json:"text"`
		} `json:"matches"`
		Truncated bool `json:"truncated"`
	} `json:"grep"`
}

func (s *Server) getFlow(a map[string]any) (string, error) {
	id, err := reqFlowID(a)
	if err != nil {
		return "", err
	}
	side := strings.ToLower(strings.TrimSpace(argStr(a, "side")))
	switch side {
	case "", "both":
		side = "both"
	case "request":
		side = "req"
	case "response":
		side = "res"
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(argInt(a, "maxBytes", 4000)))
	if off := argInt(a, "offset", 0); off > 0 {
		q.Set("offset", strconv.Itoa(off))
	}
	if g := argStr(a, "grep"); g != "" {
		q.Set("grep", g)
		q.Set("context", strconv.Itoa(argInt(a, "context", 1)))
	}
	sides := []string{side}
	if side == "both" {
		sides = []string{"req", "res"}
	}
	var b strings.Builder
	failed := 0
	var firstErr error
	for i, sd := range sides {
		if len(sides) > 1 {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(map[string]string{"req": "=== REQUEST ===\n", "res": "=== RESPONSE ===\n"}[sd])
		}
		q.Set("side", sd)
		raw, err := s.apiGet(fmt.Sprintf("/api/flows/%d/slice?%s", id, q.Encode()))
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			b.WriteString("(" + err.Error() + ")")
			continue
		}
		var sl flowSlice
		if err := json.Unmarshal([]byte(raw), &sl); err != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("decode flow slice: %w", err)
			}
			b.WriteString("(unreadable flow slice)")
			continue
		}
		writeFlowSlice(&b, id, sd, sl)
	}
	if failed == len(sides) {
		return "", firstErr // every side failed (e.g. no such flow): a tool error, not text
	}
	fmt.Fprintf(&b, "\n\nUI: %s/#flow-%d", s.base, id)
	return b.String(), nil
}

// writeFlowSlice renders one side: head, then the body window, grep matches or
// binary description, then a one-line footer saying what was shown and how to
// get more.
func writeFlowSlice(b *strings.Builder, id int, side string, sl flowSlice) {
	b.WriteString(strings.TrimRight(sl.Head, "\r\n"))
	b.WriteString("\r\n\r\n")
	size := fmt.Sprintf("%d bytes", sl.BodyBytes)
	if sl.Mime != "" {
		size += ", " + sl.Mime
	}
	if sl.Decoded {
		size += ", decoded for display"
	}
	if sl.Capped {
		size += ", decoded view capped"
	}
	switch {
	case sl.BodyBytes == 0:
		b.WriteString("[no body]")
	case sl.Grep != nil:
		fmt.Fprintf(b, "[grep %q: %d matching line(s) in %s]", sl.Grep.Pattern, sl.Grep.Count, size)
		for _, m := range sl.Grep.Matches {
			fmt.Fprintf(b, "\n@ line %d (byte %d):\n%s", m.Line, m.Offset, m.Text)
		}
		if sl.Grep.Truncated {
			fmt.Fprintf(b, "\n[only the first %d matches shown — narrow the pattern]", len(sl.Grep.Matches))
		}
	case sl.Binary:
		hash := sl.BodyHash
		if len(hash) > 16 {
			hash = hash[:16] + "…"
		}
		fmt.Fprintf(b, "[binary body: %s, sha256 %s — not shown as text; hex of the first bytes:]\n%s", size, hash, strings.TrimRight(sl.HexHead, "\n"))
	default:
		b.WriteString(sl.Text)
		end := sl.Offset + sl.Shown
		switch {
		case sl.Shown == 0 && sl.Offset == 0:
			fmt.Fprintf(b, "[body: %s — not shown; pass maxBytes>0 or grep]", size)
		case sl.More:
			fmt.Fprintf(b, "\n[body bytes %d-%d of %s — call get_flow id=%d side=%s offset=%d for more, or grep=<regex> to search]", sl.Offset, end, size, id, side, sl.Next)
		case sl.Offset > 0:
			fmt.Fprintf(b, "\n[body bytes %d-%d of %s — end of body]", sl.Offset, end, size)
		}
	}
}
