package control

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/store"
)

// discoverySeeds returns path segments seen in captured history for a host —
// passive wordlist seeding from traffic already in the store.
func (h *discoveryAPI) discoverySeeds(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		httpErr(w, http.StatusBadRequest, "host required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": h.collectPathSeeds(host)})
}

func (h *discoveryAPI) collectPathSeeds(host string) []string {
	flows, _ := h.st.QueryFlowsFilter(store.FlowFilter{
		Host:         host,
		Limit:        5000,
		ExcludeFlags: store.FlagIntruder | store.FlagActiveScan,
	})
	seen := map[string]bool{}
	var out []string
	for _, fl := range flows {
		for _, seg := range pathSegments(fl.Path) {
			if seg == "" || seen[seg] {
				continue
			}
			seen[seg] = true
			out = append(out, seg)
		}
	}
	sort.Strings(out)
	return out
}

// pathSegments splits a URL path into directory/file name tokens for seeding.
func pathSegments(p string) []string {
	p = strings.Split(strings.TrimSpace(p), "?")[0]
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// discoveryScopeTargets lists base URLs from enabled include-scope rules.
func (h *discoveryAPI) discoveryScopeTargets(w http.ResponseWriter, r *http.Request) {
	rules, _ := h.st.ListScopeRules()
	seen := map[string]bool{}
	var bases []string
	for _, rule := range rules {
		if !rule.Enabled || rule.Action != "include" || rule.Host == "" {
			continue
		}
		host := strings.TrimPrefix(rule.Host, "*.")
		scheme := rule.Scheme
		if scheme == "" {
			scheme = "https"
		}
		base := scheme + "://" + host + "/"
		if seen[base] {
			continue
		}
		seen[base] = true
		bases = append(bases, base)
	}
	sort.Strings(bases)
	writeJSON(w, http.StatusOK, map[string]any{"bases": bases})
}

// discoverySuggest merges passive seeds with optional AI-ranked path guesses.
func (h *discoveryAPI) discoverySuggest(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		// Derive host from base URL if given.
		if raw := strings.TrimSpace(r.URL.Query().Get("baseUrl")); raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				host = u.Hostname()
			}
		}
	}
	if host == "" {
		httpErr(w, http.StatusBadRequest, "host or baseUrl required")
		return
	}
	seeds := h.collectPathSeeds(host)
	aiPaths, aiNote := h.aiDiscoveryPaths(host, seeds)
	merged := mergePaths(seeds, aiPaths)
	writeJSON(w, http.StatusOK, map[string]any{
		"host": host, "paths": merged, "seeds": len(seeds), "aiNote": aiNote,
	})
}

func mergePaths(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, p := range list {
			p = strings.Trim(p, "/")
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// aiDiscoveryPaths asks the configured LLM for extra path guesses (best-effort).
func (h *discoveryAPI) aiDiscoveryPaths(host string, seeds []string) ([]string, string) {
	if h.aiDisabled() {
		return nil, "AI disabled in Settings"
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		return nil, "no AI key — returning history seeds only"
	}
	sample := seeds
	if len(sample) > 40 {
		sample = sample[:40]
	}
	prompt := "Host: " + host + "\nPaths already seen in captured traffic:\n" + strings.Join(sample, "\n") +
		"\n\nSuggest up to 20 additional URL path segments (one per line, no leading slash) a pentester should brute-force on this host — admin panels, APIs, backups, dev paths. Return ONLY a JSON array of strings."
	model, _, _ := h.st.GetSetting("ai.model")
	text, err := aiassist.New(provider, key, model).Complete(
		"Return only valid JSON. No prose.", prompt)
	if err != nil {
		return nil, err.Error()
	}
	arr := extractJSONArray(text)
	if arr == "" {
		return nil, ""
	}
	var paths []string
	if json.Unmarshal([]byte(arr), &paths) != nil {
		return nil, ""
	}
	return paths, ""
}
