// Package control serves the Interceptor UI and the REST + SSE control API on
// the fixed localhost control port. It bridges the browser UI to the store and
// the intercept engine and pushes live events (captured flows, hold-queue
// changes) over Server-Sent Events.
package control

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/intruder"
	"github.com/Veyal/interceptor/internal/mcp"
	"github.com/Veyal/interceptor/internal/oob"
	"github.com/Veyal/interceptor/internal/scope"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/tlsca"
)

//go:embed ui
var uiFS embed.FS

// Rebinder lets the control plane move the proxy listener at runtime.
type Rebinder interface {
	Rebind(addr string) error // open the new listener first; keep the old one on failure
	Addr() string             // the current proxy listen address
}

// Hub is the control-plane HTTP handler and live-event broadcaster. It also
// implements the proxy's Events sink (FlowCaptured).
type Hub struct {
	st     *store.Store
	eng    *intercept.Engine
	ca     *tlsca.CA
	rebind Rebinder
	snd    *sender.Sender
	intr   *intruder.Engine
	sc     *scope.Engine
	oob    *oob.Catcher
	mux    *http.ServeMux

	// Upstream applies a chained upstream-proxy URL ("" = direct). Set by cmd.
	Upstream func(string) error
	// SetCaptureScopeOnly toggles persisting only in-scope traffic. Set by cmd.
	SetCaptureScopeOnly func(bool)
	// SetSuppressBrowserTelemetry toggles suppression of Chrome/Firefox telemetry. Set by cmd.
	SetSuppressBrowserTelemetry func(bool)

	// ChecksDir holds user-authored Starlark scanner checks ("" = none). Set by cmd.
	ChecksDir string

	// SelfAddr is this control plane's own host:port (e.g. 127.0.0.1:9966). Set by
	// cmd; the active scanner refuses to target it, so it never attacks its own API.
	SelfAddr string

	// ProjectName/ProjectDir identify the active project (Burp-style). Set by cmd;
	// surfaced at GET /api/version so the UI can show which project is loaded.
	ProjectName string
	ProjectDir  string
	// GlobalDir is ~/.interceptor (named projects live in GlobalDir/projects).
	// SwitchProject re-launches Interceptor into another project; nil if unsupported.
	GlobalDir     string
	SwitchProject func(target string) error

	mcpMu  sync.Mutex
	mcpSrv *mcp.Server // lazily built streamable-HTTP MCP front end (POST /mcp)

	as asState // active-scan state (armed/running/findings)

	updMu     sync.Mutex // update-check result (set by cmd's background check)
	updLatest string
	updAvail  bool

	mu      sync.Mutex
	clients map[chan string]struct{}
}

// New builds a Hub. eng, ca, and rebind may be nil. If eng is non-nil, the
// hub registers itself as the intercept change notifier.
func New(st *store.Store, eng *intercept.Engine, ca *tlsca.CA, rebind Rebinder, sc *scope.Engine) *Hub {
	if sc == nil {
		sc = scope.New()
	}
	h := &Hub{
		st:      st,
		eng:     eng,
		ca:      ca,
		rebind:  rebind,
		sc:      sc,
		snd:     sender.New(st, capture.New(st)),
		mux:     http.NewServeMux(),
		clients: map[chan string]struct{}{},
	}
	h.intr = intruder.New(h.snd)
	h.intr.SetBodyReader(h.bodyBytes) // lets Intruder grep response bodies
	h.intr.SetNotifier(func() { h.broadcast(map[string]any{"type": "intruder.update"}) })
	h.oob = oob.New()
	h.oob.SetNotifier(func() { h.broadcast(map[string]any{"type": "oob.update"}) })
	h.refreshScope()
	h.applySessionFromStore()
	h.routes()
	if eng != nil {
		eng.SetNotifier(h.broadcastIntercept)
		if rules, err := st.ListRules(); err == nil {
			_ = eng.SetRules(rules)
		}
	}
	return h
}

// Handler returns the control-plane HTTP handler, wrapped in the loopback/CSRF
// security guard (see securityGuard).
func (h *Hub) Handler() http.Handler { return h.securityGuard(h.mux) }

// handleMCP serves the Streamable-HTTP MCP transport. The backing mcp.Server is
// built lazily from the request's own Host so its tool calls loop back to this
// control server (the same wiring the `interceptor mcp` stdio subcommand uses).
func (h *Hub) handleMCP(w http.ResponseWriter, r *http.Request) {
	h.mcpMu.Lock()
	if h.mcpSrv == nil {
		h.mcpSrv = mcp.New("http://" + r.Host)
	}
	srv := h.mcpSrv
	h.mcpMu.Unlock()
	srv.ServeHTTP(w, r)
}

func (h *Hub) routes() {
	h.mux.HandleFunc("GET /api/flows", h.listFlows)
	h.mux.HandleFunc("GET /api/flows/{id}", h.getFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/raw", h.getFlowRaw)
	h.mux.HandleFunc("GET /api/flows/{id}/ws", h.flowWS)
	h.mux.HandleFunc("GET /api/flows/{id}/analyze", h.analyzeFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/curl", h.flowCurl)
	h.mux.HandleFunc("PUT /api/flows/{id}/note", h.setFlowNote)
	h.mux.HandleFunc("POST /api/flows/delete", h.deleteFlows)
	h.mux.HandleFunc("POST /api/flows/purge", h.purgeFlows)
	h.mux.HandleFunc("POST /api/flows/gc", h.gcBodies)
	h.mux.HandleFunc("GET /api/hosts/stats", h.hostStats)
	h.mux.HandleFunc("GET /api/endpoints", h.listEndpoints)
	h.mux.HandleFunc("GET /api/notes", h.getNotes)
	h.mux.HandleFunc("PUT /api/notes", h.putNotes)
	h.mux.HandleFunc("GET /api/rules", h.listRules)
	h.mux.HandleFunc("POST /api/rules", h.createRule)
	h.mux.HandleFunc("PUT /api/rules/{id}", h.updateRule)
	h.mux.HandleFunc("DELETE /api/rules/{id}", h.deleteRule)
	h.mux.HandleFunc("GET /api/intercept", h.getIntercept)
	h.mux.HandleFunc("POST /api/intercept/toggle", h.toggleIntercept)
	h.mux.HandleFunc("POST /api/intercept/filter", h.setInterceptFilter)
	h.mux.HandleFunc("POST /api/intercept/{id}/forward", h.forwardIntercept)
	h.mux.HandleFunc("POST /api/intercept/{id}/drop", h.dropIntercept)
	h.mux.HandleFunc("POST /api/intercept/response/toggle", h.toggleResponseIntercept)
	h.mux.HandleFunc("POST /api/intercept/response/{id}/forward", h.forwardResponse)
	h.mux.HandleFunc("POST /api/intercept/response/{id}/drop", h.dropResponse)
	h.mux.HandleFunc("GET /api/settings", h.getSettings)
	h.mux.HandleFunc("PUT /api/settings", h.putSettings)
	h.mux.HandleFunc("GET /api/sysproxy", h.getSysProxy)
	h.mux.HandleFunc("POST /api/sysproxy", h.setSysProxy)
	h.mux.HandleFunc("GET /api/session", h.getSession)
	h.mux.HandleFunc("POST /api/session", h.setSession)
	h.mux.HandleFunc("POST /api/ai/assist", h.aiAssist)
	h.mux.HandleFunc("POST /api/ai/assist/stream", h.aiAssistStream)
	h.mux.HandleFunc("POST /api/ai/actions", h.aiActions)
	h.mux.HandleFunc("GET /api/ca.crt", h.getCA)
	h.mux.HandleFunc("POST /api/repeater/send", h.repeaterSend)
	h.mux.HandleFunc("GET /api/repeater/history", h.repeaterHistory)
	h.mux.HandleFunc("POST /api/intruder/start", h.intruderStart)
	h.mux.HandleFunc("GET /api/intruder/state", h.intruderState)
	h.mux.HandleFunc("/oob/", h.oobCatch) // public: blind callbacks land here (guard-bypassed)
	h.mux.HandleFunc("GET /api/oob/state", h.oobState)
	h.mux.HandleFunc("POST /api/oob/new", h.oobNew)
	h.mux.HandleFunc("POST /api/oob/base", h.oobSetBase)
	h.mux.HandleFunc("DELETE /api/oob/interactions", h.oobClear)
	h.mux.HandleFunc("GET /api/authz", h.getAuthz)
	h.mux.HandleFunc("POST /api/authz", h.setAuthz)
	h.mux.HandleFunc("POST /api/authz/run", h.authzRun)
	h.mux.HandleFunc("POST /api/scanner/run", h.scannerRun)
	h.mux.HandleFunc("GET /api/scanner/issues", h.scannerIssues)
	h.mux.HandleFunc("GET /api/scanner/report", h.scannerReport)
	h.mux.HandleFunc("GET /api/checks", h.listChecks)
	h.mux.HandleFunc("POST /api/checks/test", h.testCheck)
	h.mux.HandleFunc("GET /api/checks/{id}", h.getCheck)
	h.mux.HandleFunc("PUT /api/checks/{id}", h.saveCheck)
	h.mux.HandleFunc("DELETE /api/checks/{id}", h.deleteCheck)
	h.mux.HandleFunc("POST /api/ws/send", h.wsSend)
	h.mux.HandleFunc("POST /api/decode", h.decode)
	h.mux.HandleFunc("GET /api/activescan", h.asGet)
	h.mux.HandleFunc("POST /api/activescan/arm", h.asArm)
	h.mux.HandleFunc("POST /api/activescan/start", h.asStart)
	h.mux.HandleFunc("POST /api/activescan/stop", h.asStop)
	h.mux.HandleFunc("GET /api/keys", h.listKeys)
	h.mux.HandleFunc("POST /api/keys", h.createKey)
	h.mux.HandleFunc("DELETE /api/keys/{id}", h.deleteKey)
	h.mux.HandleFunc("GET /api/version", h.apiVersion)
	h.mux.HandleFunc("GET /api/activity", h.listActivity)
	h.mux.HandleFunc("POST /api/activity", h.postActivity)
	h.mux.HandleFunc("DELETE /api/activity", h.clearActivity)
	h.mux.HandleFunc("GET /api/project", h.apiProject)
	h.mux.HandleFunc("POST /api/project/switch", h.switchProject)
	h.mux.HandleFunc("GET /api/reference", h.apiReference)
	h.mux.HandleFunc("GET /api/mcp", h.apiMCP)
	// Streamable-HTTP MCP transport: a remote/hosted agent can drive the engine
	// over the control port without the `interceptor mcp` stdio subcommand.
	h.mux.HandleFunc("POST /mcp", h.handleMCP)
	h.mux.HandleFunc("GET /mcp", h.handleMCP)
	h.mux.HandleFunc("OPTIONS /mcp", h.handleMCP)
	h.mux.HandleFunc("GET /api/export/har", h.exportHAR)
	h.mux.HandleFunc("POST /api/import/har", h.importHAR)
	h.mux.HandleFunc("GET /api/export/project", h.exportProject)
	h.mux.HandleFunc("POST /api/import/project", h.importProject)
	h.mux.HandleFunc("GET /api/views", h.listViews)
	h.mux.HandleFunc("POST /api/views", h.createView)
	h.mux.HandleFunc("DELETE /api/views/{id}", h.deleteView)
	h.mux.HandleFunc("GET /api/scope", h.listScope)
	h.mux.HandleFunc("POST /api/scope", h.createScope)
	h.mux.HandleFunc("PUT /api/scope/{id}", h.updateScope)
	h.mux.HandleFunc("DELETE /api/scope/{id}", h.deleteScope)
	h.mux.HandleFunc("GET /api/events", h.handleEvents)
	h.mux.HandleFunc("/", h.serveUI)
}

// ---- DTOs ----

type flowJSON struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	Method     string `json:"method"`
	Scheme     string `json:"scheme"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Mime       string `json:"mime"`
	ReqLen     int64  `json:"reqLen"`
	ResLen     int64  `json:"resLen"`
	DurationMs int64  `json:"durationMs"`
	ClientAddr string `json:"clientAddr"`
	Error      string `json:"error"`
	Flags      int64  `json:"flags"`
	Note       string `json:"note"`
}

type flowDetailJSON struct {
	flowJSON
	HTTPVersion string              `json:"httpVersion"`
	ReqHeaders  map[string][]string `json:"reqHeaders"`
	ResHeaders  map[string][]string `json:"resHeaders"`
	ReqBodyHash string              `json:"reqBodyHash"`
	ResBodyHash string              `json:"resBodyHash"`
}

type ruleJSON struct {
	ID      int64  `json:"id"`
	Ord     int    `json:"ord"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	Match   string `json:"match"`
	Replace string `json:"replace"`
}

type heldJSON struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	Raw    string `json:"raw"`
}

type interceptJSON struct {
	Enabled         bool       `json:"enabled"`
	Queue           []heldJSON `json:"queue"`
	ResponseEnabled bool       `json:"responseEnabled"`
	ResponseQueue   []heldJSON `json:"responseQueue"`
	FilterEnabled   bool       `json:"filterEnabled"`
	FilterTarget    string     `json:"filterTarget"`
	FilterPattern   string     `json:"filterPattern"`
}

type settingsJSON struct {
	ProxyAddr                   string `json:"proxyAddr"`
	InterceptEnabled            bool   `json:"interceptEnabled"`
	UpstreamProxy               string `json:"upstreamProxy"`
	AiProvider                  string `json:"aiProvider"`
	AiModel                     string `json:"aiModel"`
	AiHasKey                    bool   `json:"aiHasKey"` // never returns the key itself
	CaptureScopeOnly            bool   `json:"captureScopeOnly"`
	SuppressBrowserTelemetry    bool   `json:"suppressBrowserTelemetry"`
}

func toFlowJSON(f *store.Flow) flowJSON {
	return flowJSON{
		ID: f.ID, TS: f.TS.UnixMilli(), Method: f.Method, Scheme: f.Scheme, Host: f.Host,
		Port: f.Port, Path: f.Path, Status: f.Status, Mime: f.Mime, ReqLen: f.ReqLen,
		ResLen: f.ResLen, DurationMs: f.DurationMs, ClientAddr: f.ClientAddr, Error: f.Error, Flags: f.Flags,
		Note: f.Note,
	}
}

// setFlowNote attaches (or clears, with an empty string) a free-text note on a
// flow — used by the inspector's Notes field and the MCP set_note tool. The
// change is broadcast so every connected client updates the row and open detail.
func (h *Hub) setFlowNote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := h.st.SetFlowNote(id, in.Note); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if f, err := h.st.GetFlow(id); err == nil {
		h.FlowUpdated(f)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFlows removes the listed flows from History. Bodies are content-addressed
// and shared, so only the metadata rows are dropped. Clients are told to reload.
func (h *Hub) deleteFlows(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	n, err := h.st.DeleteFlows(in.IDs)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > 0 {
		h.broadcast(map[string]any{"type": "flow.new"}) // reuse the reload signal
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// listEndpoints returns the unique-endpoint map for the attack-surface view —
// proxied/manual traffic aggregated by (host, method, path); bulk attack traffic
// (Intruder / active scan) is excluded as noise.
func (h *Hub) listEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	eps, err := h.st.Endpoints(store.EndpointFilter{
		Host:         q.Get("host"),
		Search:       q.Get("search"),
		ExcludeFlags: store.FlagIntruder | store.FlagActiveScan,
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if eps == nil {
		eps = []store.Endpoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": eps})
}

// ---- Flows ----

func (h *Hub) listFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.FlowFilter{
		Limit:        atoiOr(q.Get("limit"), 200),
		BeforeID:     int64(atoiOr(q.Get("before"), 0)),
		Method:       q.Get("method"),
		Host:         q.Get("host"),
		Search:       q.Get("search"),
		Scheme:       q.Get("scheme"),
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan, // these have their own views
	}
	// AI-originated sends carry FlagAI on top of those flags; exempt them from the
	// exclusion so the human can watch the AI work inline in History. ?ai=0 hides them.
	if q.Get("ai") != "0" {
		f.IncludeFlags = store.FlagAI
	}
	if sc := q.Get("status"); sc != "" {
		f.StatusClass = atoiOr(sc, 0)
	}
	// Negative filters (repeatable): notMethod, notHost, notPath, notStatus.
	f.NotMethods, f.NotHosts, f.NotPaths = q["notMethod"], q["notHost"], q["notPath"]
	for _, s := range q["notStatus"] {
		if n := atoiOr(s, 0); n > 0 {
			f.NotStatuses = append(f.NotStatuses, n)
		}
	}
	flows, err := h.st.QueryFlowsFilter(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	inScopeOnly := q.Get("inScope") == "1"
	out := make([]flowJSON, 0, len(flows))
	for _, fl := range flows {
		if inScopeOnly && !h.sc.InScope(fl) {
			continue
		}
		out = append(out, toFlowJSON(fl))
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": out})
}

func (h *Hub) loadFlow(w http.ResponseWriter, r *http.Request) (*store.Flow, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return nil, false
	}
	f, err := h.st.GetFlow(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return nil, false
	}
	return f, true
}

func (h *Hub) getFlow(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, flowDetailJSON{
		flowJSON:    toFlowJSON(f),
		HTTPVersion: f.HTTPVersion,
		ReqHeaders:  f.ReqHeaders,
		ResHeaders:  f.ResHeaders,
		ReqBodyHash: f.ReqBodyHash,
		ResBodyHash: f.ResBodyHash,
	})
}

func (h *Hub) getFlowRaw(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("side") == "res" {
		w.Write(h.rawResponse(f))
	} else {
		w.Write(h.rawRequest(f))
	}
}

func (h *Hub) rawRequest(f *store.Flow) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s %s\r\n", f.Method, orVal(f.Path, "/"), orVal(f.HTTPVersion, "HTTP/1.1"))
	hdr, body := decodeForDisplay(f.ReqHeaders, h.bodyBytes(f.ReqBodyHash))
	writeHeaders(&b, hdr, f.Host)
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}

func (h *Hub) rawResponse(f *store.Flow) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %d %s\r\n", orVal(f.HTTPVersion, "HTTP/1.1"), f.Status, http.StatusText(f.Status))
	// Decompress Content-Encoding (gzip/br/zstd/deflate) so the body is readable
	// rather than showing compressed bytes that look undecrypted.
	hdr, body := decodeForDisplay(f.ResHeaders, h.bodyBytes(f.ResBodyHash))
	writeHeaders(&b, hdr, "")
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}

func (h *Hub) flowWS(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	frames, err := h.st.QueryWSFrames(id, 2000)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if frames == nil {
		frames = []*store.WSFrame{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"frames": frames})
}

func (h *Hub) bodyBytes(hash string) []byte {
	if hash == "" {
		return nil
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

// ---- Rules ----

func (h *Hub) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.st.ListRules()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ruleJSON, 0, len(rules))
	for _, ru := range rules {
		out = append(out, ruleJSON(ru))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func (h *Hub) createRule(w http.ResponseWriter, r *http.Request) {
	var in ruleJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !validRule(w, in) {
		return
	}
	rule := store.Rule{Ord: in.Ord, Enabled: in.Enabled, Type: in.Type, Match: in.Match, Replace: in.Replace}
	id, err := h.st.CreateRule(&rule)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	in.ID = id
	h.refreshRules()
	writeJSON(w, http.StatusCreated, in)
}

func (h *Hub) updateRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in ruleJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	in.ID = id
	if !validRule(w, in) {
		return
	}
	if err := h.st.UpdateRule(&store.Rule{ID: id, Ord: in.Ord, Enabled: in.Enabled, Type: in.Type, Match: in.Match, Replace: in.Replace}); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.refreshRules()
	writeJSON(w, http.StatusOK, in)
}

func (h *Hub) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteRule(id); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.refreshRules()
	w.WriteHeader(http.StatusNoContent)
}

// validRule rejects unknown types and uncompilable regexes (writing the error).
func validRule(w http.ResponseWriter, in ruleJSON) bool {
	switch in.Type {
	case "req-header", "req-body", "res-header", "res-body":
	default:
		httpErr(w, http.StatusBadRequest, "type must be req-header, req-body, res-header, or res-body")
		return false
	}
	// Compile-check the regex through the engine's validation path.
	if err := (intercept.New()).SetRules([]store.Rule{{Enabled: true, Type: in.Type, Match: in.Match}}); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// refreshRules recompiles the live engine rule set and broadcasts the change.
func (h *Hub) refreshRules() {
	if h.eng != nil {
		if rules, err := h.st.ListRules(); err == nil {
			_ = h.eng.SetRules(rules)
		}
	}
	h.broadcast(map[string]any{"type": "rules.update"})
}

// ---- Intercept ----

func (h *Hub) interceptState() interceptJSON {
	out := interceptJSON{}
	if h.eng == nil {
		return out
	}
	out.Enabled = h.eng.Enabled()
	for _, held := range h.eng.Queue() {
		hj := heldJSON{ID: held.ID, Raw: string(held.Raw)}
		if held.Flow != nil {
			hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
		}
		out.Queue = append(out.Queue, hj)
	}
	out.ResponseEnabled = h.eng.ResponseEnabled()
	for _, held := range h.eng.ResponseQueue() {
		hj := heldJSON{ID: held.ID, Raw: string(held.Raw)}
		if held.Flow != nil {
			hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
		}
		out.ResponseQueue = append(out.ResponseQueue, hj)
	}
	out.FilterEnabled, out.FilterTarget, out.FilterPattern = h.eng.InterceptFilter()
	return out
}

func (h *Hub) getIntercept(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.interceptState())
}

func (h *Hub) toggleIntercept(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	h.eng.SetEnabled(in.Enabled)
	_ = h.st.SetSetting("intercept.enabled", boolToFlag(in.Enabled))
	writeJSON(w, http.StatusOK, h.interceptState())
}

// setInterceptFilter configures the conditional-intercept regex filter and
// persists it so the choice survives restarts.
func (h *Hub) setInterceptFilter(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	var in struct {
		Enabled bool   `json:"enabled"`
		Target  string `json:"target"`
		Pattern string `json:"pattern"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := h.eng.SetInterceptFilter(in.Enabled, in.Target, in.Pattern); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid regex: "+err.Error())
		return
	}
	enabled, target, pattern := h.eng.InterceptFilter()
	_ = h.st.SetSetting("intercept.filter.enabled", boolToFlag(enabled))
	_ = h.st.SetSetting("intercept.filter.target", target)
	_ = h.st.SetSetting("intercept.filter.pattern", pattern)
	writeJSON(w, http.StatusOK, h.interceptState())
}

func (h *Hub) forwardIntercept(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Raw string `json:"raw"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := h.eng.Forward(id, []byte(in.Raw)); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) dropIntercept(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.eng.Drop(id); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) toggleResponseIntercept(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	h.eng.SetResponseEnabled(in.Enabled)
	writeJSON(w, http.StatusOK, h.interceptState())
}

func (h *Hub) forwardResponse(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Raw string `json:"raw"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := h.eng.ForwardResponse(id, []byte(in.Raw)); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) dropResponse(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.eng.DropResponse(id); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Settings / CA ----

func (h *Hub) currentProxyAddr() string {
	if h.rebind != nil {
		return h.rebind.Addr()
	}
	if v, ok, _ := h.st.GetSetting("proxy.addr"); ok && v != "" {
		return v
	}
	return "127.0.0.1:8080"
}

func (h *Hub) getSettings(w http.ResponseWriter, r *http.Request) {
	up, _, _ := h.st.GetSetting("upstream.proxy")
	aiProvider, _, _ := h.st.GetSetting("ai.provider")
	if aiProvider == "" {
		aiProvider = "anthropic"
	}
	aiKey, _, _ := h.st.GetSetting("ai.apiKey")
	aiModel, _, _ := h.st.GetSetting("ai.model")
	envKey := os.Getenv("ANTHROPIC_API_KEY")
	if aiProvider == "openrouter" {
		envKey = os.Getenv("OPENROUTER_API_KEY")
	}
	scopeOnly, _, _ := h.st.GetSetting("capture.scopeOnly")
	suppressTelemetry, stOK, _ := h.st.GetSetting("capture.suppressBrowserTelemetry")
	// Default to true when the key has never been written (first run).
	suppressTelemetryOn := !stOK || suppressTelemetry == "1"
	writeJSON(w, http.StatusOK, settingsJSON{
		ProxyAddr:                h.currentProxyAddr(),
		InterceptEnabled:         h.eng != nil && h.eng.Enabled(),
		UpstreamProxy:            up,
		AiProvider:               aiProvider,
		AiModel:                  aiModel,
		AiHasKey:                 aiKey != "" || envKey != "",
		CaptureScopeOnly:         scopeOnly == "1",
		SuppressBrowserTelemetry: suppressTelemetryOn,
	})
}

func (h *Hub) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProxyAddr                   string  `json:"proxyAddr"`
		UpstreamProxy               *string `json:"upstreamProxy"` // pointer so "" can clear it
		AiProvider                  *string `json:"aiProvider"`
		AiApiKey                    *string `json:"aiApiKey"`
		AiModel                     *string `json:"aiModel"`
		CaptureScopeOnly            *bool   `json:"captureScopeOnly"`
		SuppressBrowserTelemetry    *bool   `json:"suppressBrowserTelemetry"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.AiProvider != nil {
		_ = h.st.SetSetting("ai.provider", *in.AiProvider)
	}
	if in.AiApiKey != nil {
		_ = h.st.SetSetting("ai.apiKey", *in.AiApiKey)
	}
	if in.AiModel != nil {
		_ = h.st.SetSetting("ai.model", *in.AiModel)
	}
	if in.CaptureScopeOnly != nil {
		v := "0"
		if *in.CaptureScopeOnly {
			v = "1"
		}
		_ = h.st.SetSetting("capture.scopeOnly", v)
		if h.SetCaptureScopeOnly != nil {
			h.SetCaptureScopeOnly(*in.CaptureScopeOnly)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.SuppressBrowserTelemetry != nil {
		v := "0"
		if *in.SuppressBrowserTelemetry {
			v = "1"
		}
		_ = h.st.SetSetting("capture.suppressBrowserTelemetry", v)
		if h.SetSuppressBrowserTelemetry != nil {
			h.SetSuppressBrowserTelemetry(*in.SuppressBrowserTelemetry)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.ProxyAddr != "" && in.ProxyAddr != h.currentProxyAddr() {
		// Refuse to expose the proxy on a non-loopback interface unless the
		// operator explicitly opts in. This blocks a hostile page (or a slip)
		// from rebinding the proxy to 0.0.0.0 and putting it on the network.
		if !isLoopbackHost(in.ProxyAddr) && os.Getenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND") == "" {
			httpErr(w, http.StatusBadRequest, "proxy bind address must be loopback (127.0.0.1/localhost/::1); set INTERCEPTOR_ALLOW_EXTERNAL_BIND=1 to allow external binds")
			return
		}
		if h.rebind != nil {
			if err := h.rebind.Rebind(in.ProxyAddr); err != nil {
				httpErr(w, http.StatusBadRequest, "rebind failed: "+err.Error())
				return
			}
		}
		_ = h.st.SetSetting("proxy.addr", in.ProxyAddr)
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.UpstreamProxy != nil {
		if h.Upstream != nil {
			if err := h.Upstream(*in.UpstreamProxy); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		_ = h.st.SetSetting("upstream.proxy", *in.UpstreamProxy)
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	h.getSettings(w, r)
}

func (h *Hub) getCA(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor-ca.crt"`)
	w.Write(h.ca.CertPEM())
}

// ---- UI ----

// serveUI serves the embedded UI: "/" returns the index shell and any other
// path resolves to a static asset under ui/ (app.css, js/*.js). Content types
// are set explicitly — relying on the OS mime registry is unsafe on Windows,
// where ".js" can resolve to text/plain and browsers then refuse to execute the
// ES modules.
func (h *Hub) serveUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) { // rejects "..", absolute, and other unclean paths
		http.NotFound(w, r)
		return
	}
	data, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		if name == "index.html" {
			httpErr(w, http.StatusInternalServerError, "ui missing")
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", uiContentType(name))
	// The UI is embedded and changes with every build; never let a browser serve a
	// stale shell or JS module after an upgrade (the modules have no version hash).
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// uiContentType maps a UI asset's extension to a Content-Type, independent of any
// OS mime registry (see serveUI).
func uiContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeHeaders(b *bytes.Buffer, h map[string][]string, host string) {
	if host != "" && len(h["Host"]) == 0 {
		fmt.Fprintf(b, "Host: %s\r\n", host)
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(b, "%s: %s\r\n", k, v)
		}
	}
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func orVal(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func boolToFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
