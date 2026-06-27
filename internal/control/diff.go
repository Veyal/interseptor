package control

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interceptor/internal/store"
)

// diffMaxBytes caps how many response-body bytes are compared per side, so a
// huge body can't blow up the diff output. Mirrors the maxBytes idiom used by
// get_flow and friends. The query/tool can lower it but not raise it past a
// sane ceiling.
const (
	diffDefaultMaxBytes = 4000
	diffMaxBytesCeiling = 64000
	diffMaxBodyLines    = 60 // cap on changed-body lines reported (keeps output bounded)
)

// headerDelta describes one header that differs between two flows.
type headerDelta struct {
	Name string `json:"name"`
	A    string `json:"a,omitempty"` // value in flow A ("" when added in B)
	B    string `json:"b,omitempty"` // value in flow B ("" when removed in B)
	Kind string `json:"kind"`        // added | removed | changed
}

// bodyLineDelta is one line that differs between the two bodies.
type bodyLineDelta struct {
	Line int    `json:"line"` // 1-based line number (in the longer body)
	A    string `json:"a"`    // line in flow A ("" if A had no such line)
	B    string `json:"b"`    // line in flow B ("" if B had no such line)
}

// flowDiff is the structured, deterministic comparison of two flows' responses.
type flowDiff struct {
	A          int64 `json:"a"`
	B          int64 `json:"b"`
	StatusA    int   `json:"statusA"`
	StatusB    int   `json:"statusB"`
	StatusSame bool  `json:"statusSame"`

	ResLenA     int64 `json:"resLenA"`
	ResLenB     int64 `json:"resLenB"`
	ResLenDelta int64 `json:"resLenDelta"`

	HeaderDeltas []headerDelta `json:"headerDeltas"`

	BodySame      bool            `json:"bodySame"`
	BodyDeltas    []bodyLineDelta `json:"bodyDeltas"`
	BodyTruncated bool            `json:"bodyTruncated"` // body comparison hit diffMaxBytes
	BodyMoreLines int             `json:"bodyMoreLines"` // changed lines beyond diffMaxBodyLines

	Summary string `json:"summary"` // one-line human/AI readable gist
}

// diffResponses builds a deterministic diff of two response sides: status,
// response length, headers (added/removed/changed), and a bounded line-based
// body diff. The body inputs are the (already decoded) response bodies; callers
// cap them at maxBytes before passing them in. Pure — no I/O, easy to test.
func diffResponses(aID, bID int64, statusA, statusB int, resLenA, resLenB int64,
	headersA, headersB map[string][]string, bodyA, bodyB []byte, bodyTruncated bool) flowDiff {

	d := flowDiff{
		A: aID, B: bID,
		StatusA: statusA, StatusB: statusB, StatusSame: statusA == statusB,
		ResLenA: resLenA, ResLenB: resLenB, ResLenDelta: resLenB - resLenA,
		HeaderDeltas:  diffHeaders(headersA, headersB),
		BodyTruncated: bodyTruncated,
	}

	deltas, more := diffBodyLines(string(bodyA), string(bodyB))
	d.BodyDeltas = deltas
	d.BodyMoreLines = more
	d.BodySame = len(deltas) == 0 && more == 0

	d.Summary = diffSummary(d)
	return d
}

// diffHeaders compares two header maps case-insensitively and returns a sorted,
// deterministic list of added/removed/changed headers. Multi-value headers are
// joined with ", " for comparison so order within a header doesn't matter only
// the set of values does (per HTTP semantics for most headers).
func diffHeaders(a, b map[string][]string) []headerDelta {
	canon := func(m map[string][]string) map[string]string {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[http.CanonicalHeaderKey(k)] = strings.Join(v, ", ")
		}
		return out
	}
	ca, cb := canon(a), canon(b)

	names := map[string]bool{}
	for k := range ca {
		names[k] = true
	}
	for k := range cb {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	deltas := make([]headerDelta, 0)
	for _, name := range sorted {
		va, okA := ca[name]
		vb, okB := cb[name]
		switch {
		case okA && okB && va != vb:
			deltas = append(deltas, headerDelta{Name: name, A: va, B: vb, Kind: "changed"})
		case okA && !okB:
			deltas = append(deltas, headerDelta{Name: name, A: va, Kind: "removed"})
		case !okA && okB:
			deltas = append(deltas, headerDelta{Name: name, B: vb, Kind: "added"})
		}
	}
	return deltas
}

// diffBodyLines does a simple positional line-based diff of two bodies and
// returns the differing lines (capped at diffMaxBodyLines) plus a count of how
// many further changed lines were elided. Deterministic and stdlib-only — no
// LCS, just an aligned line-by-line compare, which is enough to confirm whether
// a payload changed the response and to show what changed.
func diffBodyLines(a, b string) (deltas []bodyLineDelta, more int) {
	if a == b {
		return nil, 0
	}
	la := splitLines(a)
	lb := splitLines(b)
	n := len(la)
	if len(lb) > n {
		n = len(lb)
	}
	deltas = make([]bodyLineDelta, 0)
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x == y {
			continue
		}
		if len(deltas) >= diffMaxBodyLines {
			more++
			continue
		}
		deltas = append(deltas, bodyLineDelta{Line: i + 1, A: x, B: y})
	}
	return deltas, more
}

// splitLines splits on "\n" and trims a trailing "\r" per line so CRLF and LF
// bodies compare equal.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// diffSummary renders a compact one-liner of the diff for an AI/human to read
// at a glance.
func diffSummary(d flowDiff) string {
	var parts []string
	if d.StatusSame {
		parts = append(parts, "status "+strconv.Itoa(d.StatusA)+" (same)")
	} else {
		parts = append(parts, "status "+strconv.Itoa(d.StatusA)+"→"+strconv.Itoa(d.StatusB))
	}
	switch {
	case d.ResLenDelta > 0:
		parts = append(parts, "+"+strconv.FormatInt(d.ResLenDelta, 10)+" bytes")
	case d.ResLenDelta < 0:
		parts = append(parts, strconv.FormatInt(d.ResLenDelta, 10)+" bytes")
	default:
		parts = append(parts, "len same")
	}
	if n := len(d.HeaderDeltas); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" header change(s)")
	} else {
		parts = append(parts, "headers same")
	}
	if d.BodySame {
		parts = append(parts, "body same")
	} else {
		changed := len(d.BodyDeltas) + d.BodyMoreLines
		parts = append(parts, strconv.Itoa(changed)+" body line(s) changed")
	}
	return strings.Join(parts, "; ")
}

// renderDiffText turns a flowDiff into a human/AI-readable text block (used by
// the MCP tool, which returns text).
func renderDiffText(d flowDiff) string {
	var b strings.Builder
	b.WriteString("DIFF flow ")
	b.WriteString(strconv.FormatInt(d.A, 10))
	b.WriteString(" → flow ")
	b.WriteString(strconv.FormatInt(d.B, 10))
	b.WriteString("\n")
	b.WriteString(d.Summary)
	b.WriteString("\n")

	if len(d.HeaderDeltas) > 0 {
		b.WriteString("\nHeaders:\n")
		for _, h := range d.HeaderDeltas {
			switch h.Kind {
			case "added":
				b.WriteString("  + " + h.Name + ": " + h.B + "\n")
			case "removed":
				b.WriteString("  - " + h.Name + ": " + h.A + "\n")
			case "changed":
				b.WriteString("  ~ " + h.Name + ": " + h.A + " → " + h.B + "\n")
			}
		}
	}

	if !d.BodySame {
		b.WriteString("\nBody (changed lines):\n")
		for _, l := range d.BodyDeltas {
			b.WriteString("  @" + strconv.Itoa(l.Line) + "\n")
			b.WriteString("  - " + l.A + "\n")
			b.WriteString("  + " + l.B + "\n")
		}
		if d.BodyMoreLines > 0 {
			b.WriteString("  …" + strconv.Itoa(d.BodyMoreLines) + " more changed line(s) elided\n")
		}
		if d.BodyTruncated {
			b.WriteString("  (body comparison truncated at maxBytes — raise maxBytes to compare more)\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// diffFlows is the REST handler for GET /api/flows/diff?a=<id>&b=<id>. It loads
// both flows, caps each response body at maxBytes, computes the diff, and
// returns it as JSON. ?format=text returns the rendered text block instead.
func (h *Hub) diffFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	aID, okA := parseFlowIDParam(q.Get("a"))
	bID, okB := parseFlowIDParam(q.Get("b"))
	if !okA || !okB {
		httpErr(w, http.StatusBadRequest, "a and b are required (two integer flow ids)")
		return
	}
	fa, err := h.st.GetFlow(aID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow a not found")
		return
	}
	fb, err := h.st.GetFlow(bID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow b not found")
		return
	}

	max := diffDefaultMaxBytes
	if v := q.Get("maxBytes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			max = n
		}
	}
	if max > diffMaxBytesCeiling {
		max = diffMaxBytesCeiling
	}

	d := h.buildFlowDiff(fa, fb, max)
	if q.Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(renderDiffText(d)))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// buildFlowDiff loads + decodes both flows' response bodies (capped at max) and
// returns their diff. Shared by the REST handler and any internal caller.
func (h *Hub) buildFlowDiff(fa, fb *store.Flow, max int) flowDiff {
	_, bodyA := decodeForDisplay(fa.ResHeaders, h.bodyBytes(fa.ResBodyHash))
	_, bodyB := decodeForDisplay(fb.ResHeaders, h.bodyBytes(fb.ResBodyHash))
	truncated := false
	if len(bodyA) > max {
		bodyA = bodyA[:max]
		truncated = true
	}
	if len(bodyB) > max {
		bodyB = bodyB[:max]
		truncated = true
	}
	return diffResponses(fa.ID, fb.ID, fa.Status, fb.Status, fa.ResLen, fb.ResLen,
		fa.ResHeaders, fb.ResHeaders, bodyA, bodyB, truncated)
}

// parseFlowIDParam parses a positive int64 flow id from a query value.
func parseFlowIDParam(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
