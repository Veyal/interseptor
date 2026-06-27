package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interceptor/internal/discovery"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// discoveryState holds the per-run wiring the engine itself doesn't carry: the
// headers to replay when recording found endpoints, whether to record at all,
// and a guard so a finished run is only recorded once.
type discoveryState struct {
	mu         sync.Mutex
	headers    map[string]string
	record     bool
	autoTagAPI bool
	recordRun  int64
}

// probeFor builds the discovery transport: a direct client that does NOT follow
// redirects (so 301/302 directory hints stay visible), accepts MITM/self-signed
// certs (this is a pentest tool, by design), and caps the body it reads.
func (h *Hub) probeFor() discovery.Probe {
	cl := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // pentest tool
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return func(ctx context.Context, method, raw string, headers map[string]string) (discovery.Outcome, error) {
		req, err := http.NewRequestWithContext(ctx, method, raw, nil)
		if err != nil {
			return discovery.Outcome{}, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := cl.Do(req)
		if err != nil {
			return discovery.Outcome{}, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		length := resp.ContentLength
		if length < 0 {
			length = int64(len(body))
		}
		return discovery.Outcome{
			Status:      resp.StatusCode,
			Length:      length,
			Body:        body,
			ContentType: resp.Header.Get("Content-Type"),
			Location:    resp.Header.Get("Location"),
		}, nil
	}
}

// discInScope gates every probe URL. With no include rules configured, the
// operator-typed base URL is taken as authorization (scope only excludes). With
// includes set, the probe must match — so recursion can't wander out of scope.
func (h *Hub) discInScope(raw string) bool {
	f := flowFromURL(raw)
	if f == nil {
		return false
	}
	if !h.sc.HasIncludes() {
		return true
	}
	return h.sc.InScope(f)
}

// onDiscoveryUpdate broadcasts a run change and, when a run finishes, persists
// the discovered endpoints as flows so they show up in History and the Map.
func (h *Hub) onDiscoveryUpdate() {
	st := h.disc.State()
	h.broadcast(map[string]any{"type": "discovery.update"})
	if !st.Running && st.StartedMs != 0 {
		h.recordDiscovered(st.StartedMs, st.Results)
	}
}

// recordDiscovered re-issues each found URL through the sender (tagged
// FlagDiscovery) exactly once per run, so the endpoint and its response land in
// the store. Found sets are small (that's the point of calibration/filtering),
// so the extra requests are negligible.
func (h *Hub) recordDiscovered(runID int64, results []discovery.Result) {
	h.ds.mu.Lock()
	if h.ds.recordRun == runID || !h.ds.record {
		h.ds.mu.Unlock()
		return
	}
	h.ds.recordRun = runID
	headers := h.ds.headers
	autoTagAPI := h.ds.autoTagAPI
	h.ds.mu.Unlock()

	hdr := toHeaderValues(headers)
	go func() {
		for _, r := range results {
			if r.FlowID != 0 || r.Status == 0 || r.Error != "" {
				continue
			}
			flow, err := h.snd.Send(sender.Request{
				Method:    "GET",
				URL:       r.URL,
				Headers:   hdr,
				Flags:     store.FlagDiscovery,
				NoSession: true,
			})
			if err == nil && autoTagAPI && discovery.IsAPIPath(r.Path) {
				_, _ = h.st.AddFlowTags(flow.ID, []string{"api"})
			}
		}
	}()
}

type discoverySpecIn struct {
	BaseURL    string            `json:"baseUrl"`
	Wordlist   string            `json:"wordlist"`   // newline-separated (textarea)
	Words      []string          `json:"words"`      // optional structured form
	Extensions string            `json:"extensions"` // comma/space separated, e.g. ".php .bak"
	Threads    int               `json:"threads"`
	DelayMs    int               `json:"delayMs"`
	Recursive  bool              `json:"recursive"`
	MaxDepth   int               `json:"maxDepth"`
	MatchCodes []int             `json:"matchCodes"`
	HideCodes  []int             `json:"hideCodes"`
	FilterLen  int64             `json:"filterLen"`
	Headers    map[string]string `json:"headers"`
	Record     *bool             `json:"record"`
	AutoTagAPI *bool             `json:"autoTagApi"`
}

func (h *Hub) discoveryStart(w http.ResponseWriter, r *http.Request) {
	var in discoverySpecIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad JSON")
		return
	}
	words := in.Words
	if len(words) == 0 {
		words = strings.Split(in.Wordlist, "\n")
	}
	spec := discovery.Spec{
		BaseURL:    in.BaseURL,
		Words:      words,
		Extensions: splitExtensions(in.Extensions),
		Threads:    in.Threads,
		DelayMs:    in.DelayMs,
		Recursive:  in.Recursive,
		MaxDepth:   in.MaxDepth,
		MatchCodes: in.MatchCodes,
		HideCodes:  in.HideCodes,
		FilterLen:  in.FilterLen,
		Headers:    in.Headers,
	}

	record := true
	if in.Record != nil {
		record = *in.Record
	}
	autoTagAPI := true
	if in.AutoTagAPI != nil {
		autoTagAPI = *in.AutoTagAPI
	}
	spec.AutoTagAPI = autoTagAPI
	h.ds.mu.Lock()
	h.ds.headers = in.Headers
	h.ds.record = record
	h.ds.autoTagAPI = autoTagAPI
	h.ds.mu.Unlock()

	if err := h.disc.Start(spec); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, h.disc.State())
}

func (h *Hub) discoveryStop(w http.ResponseWriter, r *http.Request) {
	h.disc.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"stopping": true})
}

func (h *Hub) discoveryStateHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.disc.State())
}

// discoveryRecord persists one found URL as a flow when recording is enabled,
// and best-effort auto-tags it `api` when the path looks like API surface (so
// operators can filter API endpoints from static assets). Tagging never affects
// the run: a tag failure is silently ignored and the flow id is still returned.
func (h *Hub) discoveryRecord(r discovery.Result) int64 {
	h.ds.mu.Lock()
	record := h.ds.record
	autoTagAPI := h.ds.autoTagAPI
	headers := h.ds.headers
	h.ds.mu.Unlock()
	if !record {
		return 0
	}
	flow, err := h.snd.Send(sender.Request{
		Method:    "GET",
		URL:       r.URL,
		Headers:   toHeaderValues(headers),
		Flags:     store.FlagDiscovery,
		NoSession: true,
	})
	if err != nil {
		return 0
	}
	if autoTagAPI && discovery.IsAPIPath(r.Path) {
		_, _ = h.st.AddFlowTags(flow.ID, []string{"api"})
	}
	return flow.ID
}

type discoveryInspectIn struct {
	URL string `json:"url"`
}

// discoveryInspect re-issues one discovered URL through the sender so the UI
// can open the flow inspect modal even when the run did not record hits.
func (h *Hub) discoveryInspect(w http.ResponseWriter, r *http.Request) {
	var in discoveryInspectIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad JSON")
		return
	}
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		httpErr(w, http.StatusBadRequest, "url required")
		return
	}
	h.ds.mu.Lock()
	headers := h.ds.headers
	h.ds.mu.Unlock()
	flow, err := h.snd.Send(sender.Request{
		Method:    "GET",
		URL:       raw,
		Headers:   toHeaderValues(headers),
		Flags:     store.FlagDiscovery,
		NoSession: true,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flowId": flow.ID})
}

// discoveryWordlist serves the built-in default wordlist so the UI can prefill
// the textarea — users can edit or replace it wholesale.
func (h *Hub) discoveryWordlist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, defaultWordlist)
}

// ---- helpers ----

// flowFromURL builds the minimal store.Flow that scope matching needs.
func flowFromURL(raw string) *store.Flow {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil
	}
	port := 0
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	} else if strings.EqualFold(u.Scheme, "https") {
		port = 443
	} else {
		port = 80
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	return &store.Flow{Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: path}
}

func toHeaderValues(in map[string]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = []string{v}
	}
	return out
}

// splitExtensions parses ".php, .bak  html" into ["php"→".php", …]. The engine
// normalizes a leading dot, so bare "html" is accepted too.
func splitExtensions(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	return fields
}
