package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Evidence renders are generated PNGs drawn from recorded data. They are never
// browser proof; the tool descriptions say so explicitly.
const evidenceRenderDisclaimer = "These are generated renders (source=evidence_render), not browser proof: they never count as real visual proof, so still attach a real screenshot with add_finding_image when the claim is visual."

// intruderRenderKinds maps the short tool kind to the REST/render kind.
var intruderRenderKinds = map[string]string{
	"timeline":     "intruder_timeline",
	"distribution": "intruder_distribution",
	"race":         "intruder_race",
	"strip":        "intruder_strip",
}

var evidenceRenderKinds = map[string]bool{
	"authz_matrix": true, "flow_diff": true, "flow_waterfall": true, "finding_chain": true,
}

// renderedPayload is the JSON a render GET returns; a bare PNG body is also accepted.
type renderedPayload struct {
	PNG        string `json:"png"`
	PNGOmitted bool   `json:"pngOmitted"` // server withheld the PNG because it is over the inline limit
	Bytes      int    `json:"bytes"`
	Alt        string `json:"alt"`
	Summary    string `json:"summary"`
	Kind       string `json:"kind"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// inlineRenderQuery asks the server for the JSON form carrying alt text,
// summary and (when small enough) the base64 PNG. The server caps inline PNGs
// at 1 MiB, far under the MCP response limit, so a render is never silently
// dropped by the client reader.
const inlineRenderQuery = "format=json&png=1"

// formatRenderResult turns a render GET response into bounded text with a data URI.
func formatRenderResult(raw, kind, restPath string) (string, error) {
	var rp renderedPayload
	if strings.HasPrefix(raw, "\x89PNG") {
		rp.PNG = base64.StdEncoding.EncodeToString([]byte(raw))
		rp.Alt = "Generated " + kind + " render (no alt text returned by server)."
	} else if err := json.Unmarshal([]byte(raw), &rp); err != nil || (rp.PNG == "" && !rp.PNGOmitted) {
		return "", fmt.Errorf("unexpected render response from %s", restPath)
	}
	if rp.Kind == "" {
		rp.Kind = kind
	}
	if rp.PNGOmitted {
		var b strings.Builder
		fmt.Fprintf(&b, "Generated evidence render (%s), not a browser screenshot.\n", rp.Kind)
		fmt.Fprintf(&b, "alt: %s\n", rp.Alt)
		if rp.Summary != "" {
			fmt.Fprintf(&b, "summary: %s\n", rp.Summary)
		}
		fmt.Fprintf(&b, "The PNG is %d bytes, over the inline limit, so it is not embedded. Re-call with findingId to attach it, or fetch %s from the control API.", rp.Bytes, restPath)
		return b.String(), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Generated evidence render (%s), not a browser screenshot.\n", rp.Kind)
	if rp.Width > 0 && rp.Height > 0 {
		fmt.Fprintf(&b, "size=%dx%d\n", rp.Width, rp.Height)
	}
	fmt.Fprintf(&b, "alt: %s\n", rp.Alt)
	if rp.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", rp.Summary)
	}
	fmt.Fprintf(&b, "mime=image/png\nURL: %s\ndata:image/png;base64,%s\n\n", restPath, rp.PNG)
	b.WriteString("Tip: re-call with findingId to attach it as source=evidence_render; add_finding_image with a real screenshot is still required for visual proof.")
	return b.String(), nil
}

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func (s *Server) registerEvidenceRenderTools() {
	s.add("render_intruder_preview",
		"Render a recorded Intruder run as a PNG: kind=timeline (rate-limit/lockout waterfall), distribution (status/length clusters and outliers), race (launch spread and duplicated outcomes) or strip (per-payload heat strip). Drawn only from recorded run data; race views state separate connections, not single-packet sync. "+evidenceRenderDisclaimer+" Without findingId returns alt text, a summary and a base64 data URI; with findingId attaches it to that finding. attackId defaults to 'latest' (use the runId from start_intruder/intruder_state).",
		obj(map[string]any{
			"attackId":  p("string", "Intruder run id (runId) or 'latest' (default)"),
			"kind":      p("string", "timeline | distribution | race | strip"),
			"findingId": p("integer", "if set, attach the PNG to this finding"),
			"caption":   pt("string"),
			"role":      p("string", "context|setup|baseline|action|result|control|retest|observation"),
			"proof":     p("string", "what the recorded data in this render establishes"),
			"mask":      p("boolean", "mask payloads and extracted values as [len N #digest] (default on)"),
			"unmask":    p("boolean", "show raw payloads and extracted values; they are often credentials, so only when the finding needs them"),
			"expected":  p("integer", "race: how many requests the application should have accepted, drawn as a baseline"),
		}, "kind"),
		func(a map[string]any) (string, error) {
			short := strings.ToLower(strings.TrimSpace(argStr(a, "kind")))
			kind, ok := intruderRenderKinds[short]
			if !ok {
				return "", fmt.Errorf("kind must be one of timeline, distribution, race, strip (got %q)", argStr(a, "kind"))
			}
			attack := strings.TrimSpace(argStr(a, "attackId"))
			if attack == "" {
				attack = "latest"
			}
			if fid := argInt(a, "findingId", 0); fid > 0 {
				body := map[string]any{"kind": kind, "attackId": attack, "caption": argStr(a, "caption"),
					"role": argStr(a, "role"), "proof": argStr(a, "proof")}
				if argBool(a, "mask", false) {
					body["mask"] = true
				}
				if argBool(a, "unmask", false) {
					body["unmask"] = true
				}
				if n := argInt(a, "expected", 0); n > 0 {
					body["expected"] = n
				}
				return s.api(http.MethodPost, fmt.Sprintf("/api/findings/%d/evidence-render", fid), body)
			}
			q := url.Values{}
			q.Set("kind", kind)
			if argBool(a, "mask", false) {
				q.Set("mask", "1")
			}
			if argBool(a, "unmask", false) {
				q.Set("unmask", "1")
			}
			if n := argInt(a, "expected", 0); n > 0 {
				q.Set("expected", strconv.Itoa(n))
			}
			path := fmt.Sprintf("/api/intruder/attacks/%s/render", url.PathEscape(attack))
			raw, err := s.apiGet(path + "?" + q.Encode() + "&" + inlineRenderQuery)
			if err != nil {
				return "", err
			}
			return formatRenderResult(raw, kind, path+"?"+q.Encode())
		})

	s.add("render_evidence",
		"Render recorded data as an evidence PNG: kind=authz_matrix (identities x requests, pass runId), flow_diff (pass flowIdA and flowIdB), flow_waterfall (pass flowIds in order) or finding_chain (pass findingIds). "+evidenceRenderDisclaimer+" Without findingId returns alt text, a summary and a base64 data URI; with findingId attaches it to that finding.",
		obj(map[string]any{
			"kind":        p("string", "authz_matrix | flow_diff | flow_waterfall | finding_chain"),
			"runId":       p("string", "authz run id (authz_matrix)"),
			"flowIdA":     p("integer", "first flow (flow_diff)"),
			"flowIdB":     p("integer", "second flow (flow_diff)"),
			"flowIds":     map[string]any{"type": "array", "items": pt("integer"), "description": "flows in sequence order (flow_waterfall)"},
			"findingIds":  map[string]any{"type": "array", "items": pt("integer"), "description": "findings to chain (finding_chain)"},
			"title":       pt("string"),
			"includeBody": p("boolean", "flow_diff: draw redacted response-body lines (default off: bodies can carry personal data)"),
			"findingId":   p("integer", "if set, attach the PNG to this finding"),
			"caption":     pt("string"),
			"role":        p("string", "context|setup|baseline|action|result|control|retest|observation"),
			"proof":       p("string", "what the recorded data in this render establishes"),
		}, "kind"),
		func(a map[string]any) (string, error) {
			kind := strings.ToLower(strings.TrimSpace(argStr(a, "kind")))
			if !evidenceRenderKinds[kind] {
				return "", fmt.Errorf("kind must be one of authz_matrix, flow_diff, flow_waterfall, finding_chain (got %q)", argStr(a, "kind"))
			}
			if fid := argInt(a, "findingId", 0); fid > 0 {
				body := map[string]any{"kind": kind, "caption": argStr(a, "caption"),
					"role": argStr(a, "role"), "proof": argStr(a, "proof")}
				for _, k := range []string{"runId", "flowIdA", "flowIdB", "flowIds", "findingIds", "title", "includeBody"} {
					if v, ok := a[k]; ok && v != nil {
						body[k] = v
					}
				}
				return s.api(http.MethodPost, fmt.Sprintf("/api/findings/%d/evidence-render", fid), body)
			}
			q := url.Values{}
			q.Set("kind", kind)
			setIf(q, "runId", argStr(a, "runId"))
			setIf(q, "title", argStr(a, "title"))
			if argBool(a, "includeBody", false) {
				q.Set("includeBody", "1")
			}
			for _, k := range []string{"flowIdA", "flowIdB"} {
				if n := argInt(a, k, 0); n > 0 {
					q.Set(k, strconv.Itoa(n))
				}
			}
			for _, k := range []string{"flowIds", "findingIds"} {
				if ids := argIntList(a, k); len(ids) > 0 {
					q.Set(k, strings.Join(ids, ","))
				}
			}
			raw, err := s.apiGet("/api/evidence-render?" + q.Encode() + "&" + inlineRenderQuery)
			if err != nil {
				return "", err
			}
			return formatRenderResult(raw, kind, "/api/evidence-render?"+q.Encode())
		})
}

// argIntList reads an array of integers (or a comma-separated string) as decimal strings.
func argIntList(a map[string]any, key string) []string {
	var out []string
	switch v := a[key].(type) {
	case []any:
		for _, el := range v {
			if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(el))); err == nil && n > 0 {
				out = append(out, strconv.Itoa(n))
			}
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
				out = append(out, strconv.Itoa(n))
			}
		}
	}
	return out
}
