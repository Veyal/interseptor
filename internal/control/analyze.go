package control

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

var reqNotableHeaders = []string{"Authorization", "Cookie", "X-Api-Key", "X-Auth-Token", "Content-Type", "Origin", "Referer", "User-Agent"}
var resNotableHeaders = []string{"Set-Cookie", "Content-Security-Policy", "Strict-Transport-Security", "Access-Control-Allow-Origin", "Server", "X-Powered-By", "Content-Type", "Location", "Www-Authenticate"}

// analyzeFlow returns a compact, decision-ready summary of a flow: where it is,
// notable security-relevant headers, parameters (injection points), passive
// scanner hits, and whether it's in scope. Built for an AI agent so it doesn't
// have to re-fetch and parse the raw exchange.
func (h *flowAPI) analyzeFlow(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	issues := scanner.AnalyzeWithDisabled(f, h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash), h.checksDisabledSet())
	findings := make([]map[string]string, 0, len(issues))
	for _, is := range issues {
		findings = append(findings, map[string]string{"severity": is.Severity, "title": is.Title})
	}

	summary := map[string]any{
		"id": f.ID, "method": f.Method, "url": analyzeURL(f), "status": f.Status,
		"scheme": f.Scheme, "host": f.Host, "mime": f.Mime,
		"reqLen": f.ReqLen, "resLen": f.ResLen, "durationMs": f.DurationMs,
		"inScope":                h.sc.InScope(f),
		"isWebSocket":            f.Flags&store.FlagWebSocket != 0,
		"isTLSFailed":            f.Flags&store.FlagTLSFailed != 0,
		"queryParams":            queryParamNames(f.Path),
		"notableRequestHeaders":  pickHeaders(f.ReqHeaders, reqNotableHeaders),
		"notableResponseHeaders": pickHeaders(f.ResHeaders, resNotableHeaders),
		"scannerFindings":        findings,
	}
	writeJSON(w, http.StatusOK, summary)
}

func analyzeURL(f *store.Flow) string {
	host := f.Host
	if !((f.Scheme == "https" && f.Port == 443) || (f.Scheme == "http" && f.Port == 80) || f.Port == 0) {
		host += ":" + strconv.Itoa(f.Port)
	}
	return f.Scheme + "://" + host + orVal(f.Path, "/")
}

func queryParamNames(path string) []string {
	u, err := url.Parse("http://x" + path)
	if err != nil {
		return []string{}
	}
	names := make([]string, 0)
	for k := range u.Query() {
		names = append(names, k)
	}
	return names
}

func pickHeaders(h map[string][]string, names []string) map[string]string {
	hdr := http.Header(h)
	out := map[string]string{}
	for _, n := range names {
		if v := hdr.Get(n); v != "" {
			if len(v) > 160 {
				v = v[:160] + "…"
			}
			out[n] = v
		}
	}
	return out
}
