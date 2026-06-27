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
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/discovery"
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
	hi     *humanInput // pending AI→human input prompts (request_human_input)
	disc   *discovery.Engine
	ds     discoveryState
	mux    *http.ServeMux

	// Upstream applies a chained upstream-proxy URL ("" = direct). Set by cmd.
	Upstream func(string) error
	// SetCaptureScopeOnly toggles persisting only in-scope traffic. Set by cmd.
	SetCaptureScopeOnly func(bool)
	// SetSuppressBrowserTelemetry toggles suppression of Chrome/Firefox telemetry. Set by cmd.
	SetSuppressBrowserTelemetry func(bool)

	// ChecksDir holds user-authored Starlark scanner checks (global, shared across
	// projects — typically ~/.interceptor/checks). Set by cmd.
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

	mcpMu       sync.Mutex
	mcpSrv      *mcp.Server // lazily built streamable-HTTP MCP front end (POST /mcp)
	mcpKeysSeen atomic.Bool // last-known "API keys exist" — mcpAuthorized fails closed on a store error once true

	as asState // active-scan state (armed/running/findings)

	updMu     sync.Mutex // update-check result (set by cmd's background check)
	updLatest string
	updAvail  bool

	mu      sync.Mutex
	clients map[chan string]struct{}

	epsCache endpointsCache

	wsMu     sync.Mutex
	wsTimers map[int64]*time.Timer // debounce ws.frame SSE per flow
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
	h.hi = newHumanInput()
	h.disc = discovery.New()
	h.disc.SetProbe(h.probeFor())
	h.disc.SetScope(h.discInScope)
	h.disc.SetNotifier(h.onDiscoveryUpdate)
	h.disc.SetRecorder(h.discoveryRecord)
	h.wireSessionRefresh()
	h.wireSessionScope()
	h.refreshScope()
	h.applySessionFromStore()
	h.routes()
	if eng != nil {
		eng.SetNotifier(h.broadcastIntercept)
		if rules, err := st.ListRules(); err == nil {
			_ = eng.SetRules(rules)
		}
	}
	h.snd.SetOnPersist(h.FlowCaptured)
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
	h.mux.HandleFunc("GET /api/flows/inscope", h.trafficInScope)
	h.mux.HandleFunc("GET /api/params", h.listParams)
	h.mux.HandleFunc("GET /api/flows/{id}", h.getFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/raw", h.getFlowRaw)
	h.mux.HandleFunc("GET /api/flows/{id}/body", h.getFlowBody)
	h.mux.HandleFunc("GET /api/flows/{id}/ws", h.flowWS)
	h.mux.HandleFunc("GET /api/flows/{id}/analyze", h.analyzeFlow)
	h.mux.HandleFunc("GET /api/flows/{id}/curl", h.flowCurl)
	h.mux.HandleFunc("PUT /api/flows/{id}/note", h.setFlowNote)
	h.mux.HandleFunc("PUT /api/flows/{id}/tags", h.setFlowTags)
	h.mux.HandleFunc("POST /api/flows/tags", h.addFlowTagsBulk)
	h.mux.HandleFunc("GET /api/tags", h.listTags)
	h.mux.HandleFunc("PUT /api/tags/{tag}/color", h.setTagColor)
	h.mux.HandleFunc("POST /api/flows/delete", h.deleteFlows)
	h.mux.HandleFunc("POST /api/flows/purge", h.purgeFlows)
	h.mux.HandleFunc("POST /api/flows/gc", h.gcBodies)
	h.mux.HandleFunc("GET /api/hosts/stats", h.hostStats)
	h.mux.HandleFunc("GET /api/endpoints", h.listEndpoints)
	h.mux.HandleFunc("GET /api/notes", h.getNotes)
	h.mux.HandleFunc("PUT /api/notes", h.putNotes)
	h.mux.HandleFunc("POST /api/notes/images", h.postNotesImage)
	h.mux.HandleFunc("GET /api/notes/images/{id}", h.getNotesImage)
	h.mux.HandleFunc("GET /api/rules", h.listRules)
	h.mux.HandleFunc("POST /api/rules", h.createRule)
	h.mux.HandleFunc("PUT /api/rules/{id}", h.updateRule)
	h.mux.HandleFunc("DELETE /api/rules/{id}", h.deleteRule)
	h.mux.HandleFunc("GET /api/intercept", h.getIntercept)
	h.mux.HandleFunc("GET /api/intercept/held/{id}/raw", h.getInterceptHeldRaw)
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
	h.mux.HandleFunc("POST /api/session/login/run", h.runLoginMacro)
	h.mux.HandleFunc("POST /api/session/login/test", h.testLoginMacro)
	h.mux.HandleFunc("POST /api/session/login/from-flow/{id}", h.loginMacroFromFlow)
	h.mux.HandleFunc("POST /api/ai/notes/organize", h.aiNotesOrganize)
	h.mux.HandleFunc("POST /api/ai/notes/organize/stream", h.aiNotesOrganizeStream)
	h.mux.HandleFunc("POST /api/ai/checks/generate", h.aiChecksGenerate)
	h.mux.HandleFunc("POST /api/ai/assist", h.aiAssist)
	h.mux.HandleFunc("POST /api/ai/assist/stream", h.aiAssistStream)
	h.mux.HandleFunc("POST /api/ai/actions", h.aiActions)
	h.mux.HandleFunc("GET /api/ai/openrouter/models", h.aiOpenRouterModels)
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
	h.mux.HandleFunc("GET /api/authz/flow-auth/{id}", h.authzFlowAuth)
	h.mux.HandleFunc("POST /api/authz/check-sessions", h.authzCheckSessions)
	h.mux.HandleFunc("POST /api/authz/run", h.authzRun)
	h.mux.HandleFunc("POST /api/authz/cross-host-replay", h.authzCrossHostReplay)
	h.mux.HandleFunc("POST /api/discovery/start", h.discoveryStart)
	h.mux.HandleFunc("POST /api/discovery/stop", h.discoveryStop)
	h.mux.HandleFunc("GET /api/discovery/state", h.discoveryStateHandler)
	h.mux.HandleFunc("GET /api/discovery/wordlist", h.discoveryWordlist)
	h.mux.HandleFunc("GET /api/discovery/seeds", h.discoverySeeds)
	h.mux.HandleFunc("GET /api/discovery/suggest", h.discoverySuggest)
	h.mux.HandleFunc("GET /api/discovery/scope-targets", h.discoveryScopeTargets)
	h.mux.HandleFunc("POST /api/discovery/inspect", h.discoveryInspect)
	h.mux.HandleFunc("POST /api/scanner/run", h.scannerRun)
	h.mux.HandleFunc("GET /api/scanner/issues", h.scannerIssues)
	h.mux.HandleFunc("GET /api/scanner/report", h.scannerReport)
	h.mux.HandleFunc("GET /api/findings", h.listFindings)
	h.mux.HandleFunc("GET /api/findings/report", h.findingsReport)
	h.mux.HandleFunc("POST /api/findings", h.createFinding)
	h.mux.HandleFunc("GET /api/findings/{id}", h.getFinding)
	h.mux.HandleFunc("PATCH /api/findings/{id}", h.updateFinding)
	h.mux.HandleFunc("DELETE /api/findings/{id}", h.deleteFinding)
	h.mux.HandleFunc("POST /api/findings/{id}/flows", h.attachFindingFlow)
	h.mux.HandleFunc("DELETE /api/findings/{id}/flows/{flowId}", h.detachFindingFlow)
	h.mux.HandleFunc("GET /api/checks", h.listChecks)
	h.mux.HandleFunc("PUT /api/checks/disabled", h.setChecksDisabled)
	h.mux.HandleFunc("GET /api/checks/reference", h.checksReference)
	h.mux.HandleFunc("POST /api/checks/test", h.testCheck)
	h.mux.HandleFunc("GET /api/checks/{id}", h.getCheck)
	h.mux.HandleFunc("PUT /api/checks/{id}", h.saveCheck)
	h.mux.HandleFunc("DELETE /api/checks/{id}", h.deleteCheck)
	h.mux.HandleFunc("POST /api/ws/send", h.wsSend)
	h.mux.HandleFunc("POST /api/decode", h.decode)
	h.mux.HandleFunc("GET /api/activescan", h.asGet)
	h.mux.HandleFunc("GET /api/activescan/history", h.activescanHistory)
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
	h.mux.HandleFunc("POST /api/human-input", h.createHumanInput)
	h.mux.HandleFunc("GET /api/human-input", h.listHumanInput)
	h.mux.HandleFunc("GET /api/human-input/{id}", h.getHumanInput)
	h.mux.HandleFunc("POST /api/human-input/{id}/respond", h.respondHumanInput)
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
	Flags      int64    `json:"flags"`
	Note       string   `json:"note"`
	Tags       []string `json:"tags,omitempty"`
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
	Raw    string `json:"raw,omitempty"`
	Len    int    `json:"len,omitempty"`
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
	ProxyAddr                string `json:"proxyAddr"`
	InterceptEnabled         bool   `json:"interceptEnabled"`
	UpstreamProxy            string `json:"upstreamProxy"`
	AiProvider               string `json:"aiProvider"`
	AiModel                  string `json:"aiModel"`
	AiHasKey                 bool   `json:"aiHasKey"` // never returns the key itself
	AiDisabled               bool   `json:"aiDisabled"`
	OobEnabled               bool   `json:"oobEnabled"`
	CaptureScopeOnly         bool   `json:"captureScopeOnly"`
	SuppressBrowserTelemetry bool   `json:"suppressBrowserTelemetry"`
}

func toFlowJSON(f *store.Flow) flowJSON {
	return flowJSON{
		ID: f.ID, TS: f.TS.UnixMilli(), Method: f.Method, Scheme: f.Scheme, Host: f.Host,
		Port: f.Port, Path: f.Path, Status: f.Status, Mime: f.Mime, ReqLen: f.ReqLen,
		ResLen: f.ResLen, DurationMs: f.DurationMs, ClientAddr: f.ClientAddr, Error: f.Error, Flags: f.Flags,
		Note: f.Note, Tags: f.Tags,
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

// reTagColor restricts tag colors to a hex value (safe to interpolate into CSS).
var reTagColor = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

// broadcastFlowTags reloads a flow (with tags) and pushes it to clients so the row
// chips update live.
func (h *Hub) broadcastFlowTags(id int64) {
	if f, err := h.st.GetFlow(id); err == nil {
		_ = h.st.AttachTags([]*store.Flow{f})
		h.FlowUpdated(f)
	}
}

// setFlowTags replaces a flow's tag set ({"tags":[...]}). Used by the inspector and
// the right-click "Tag…" action.
func (h *Hub) setFlowTags(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	tags, err := h.st.SetFlowTags(id, in.Tags)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcastFlowTags(id)
	h.broadcast(map[string]any{"type": "tags.update"})
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// addFlowTagsBulk adds or removes tags on many flows at once
// ({"flowIds":[...],"add":[...],"remove":[...]}) — for tagging a multi-selection from History.
func (h *Hub) addFlowTagsBulk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowIDs []int64  `json:"flowIds"`
		Add     []string `json:"add"`
		Remove  []string `json:"remove"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if len(in.FlowIDs) == 0 {
		httpErr(w, http.StatusBadRequest, "flowIds required")
		return
	}
	if len(in.Add) == 0 && len(in.Remove) == 0 {
		httpErr(w, http.StatusBadRequest, "add or remove required")
		return
	}
	if len(in.FlowIDs) > maxBulkItems {
		httpErr(w, http.StatusBadRequest, "too many flows")
		return
	}
	for _, id := range in.FlowIDs {
		if len(in.Add) > 0 {
			if _, err := h.st.AddFlowTags(id, in.Add); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		for _, t := range store.NormalizeTags(in.Remove) {
			if err := h.st.RemoveFlowTag(id, t); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		h.broadcastFlowTags(id)
	}
	h.broadcast(map[string]any{"type": "tags.update"})
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.FlowIDs)})
}

// listTags returns every tag in use with flow counts and colors.
func (h *Hub) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.st.DistinctTags()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// setTagColor sets (or clears) a tag's display color ({"color":"#rrggbb"}).
func (h *Hub) setTagColor(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var in struct {
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Color != "" && !reTagColor.MatchString(in.Color) {
		httpErr(w, http.StatusBadRequest, "color must be a hex like #4aa8ff")
		return
	}
	if err := h.st.SetTagColor(tag, in.Color); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "tags.update"})
	w.WriteHeader(http.StatusNoContent)
}

// deleteFlows removes the listed flows from History. Bodies are content-addressed
// and shared, so only the metadata rows are dropped. Clients are told to reload.
// maxBulkItems bounds the id/host array on bulk delete/purge. Even within the
// 128 MiB body cap, a giant array amplifies ~10× via make([]any, len) and the
// SQL placeholder string; no legitimate UI action targets this many at once.
const maxBulkItems = 100000

func (h *Hub) deleteFlows(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if len(in.IDs) > maxBulkItems {
		httpErr(w, http.StatusBadRequest, "too many ids")
		return
	}
	n, err := h.st.DeleteFlows(in.IDs)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > 0 {
		h.epsCache.invalidate()
		h.broadcast(map[string]any{"type": "flow.new"}) // reuse the reload signal
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// listEndpoints returns the unique-endpoint map for the attack-surface view —
// proxied/manual traffic aggregated by (host, method, path); bulk attack traffic
// (Intruder / active scan) is excluded as noise.
func (h *Hub) listEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EndpointFilter{
		Host:         q.Get("host"),
		Search:       q.Get("search"),
		SearchScope:  q.Get("searchScope"),
		ExcludeFlags: store.FlagIntruder | store.FlagActiveScan,
		Tag:          q.Get("tag"),
	}
	key := endpointsCacheKey(f)
	if eps, note, ok := h.epsCache.get(key); ok {
		out := map[string]any{"endpoints": eps}
		if note != "" {
			out["searchNote"] = note
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	eps, note, err := h.st.Endpoints(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if eps == nil {
		eps = []store.Endpoint{}
	}
	h.epsCache.set(key, eps, note)
	out := map[string]any{"endpoints": eps}
	if note != "" {
		out["searchNote"] = note
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- Flows ----

// flowSourceFilters parses History source toggles. Default: both manual (proxy) and
// AI sends. Legacy: ?ai=0 hides AI; ?onlyAi=1 is AI-only.
func flowSourceFilters(q url.Values) (manual, ai bool) {
	manual, ai = true, true
	if q.Get("onlyAi") == "1" {
		return false, true
	}
	if v := q.Get("manual"); v != "" {
		manual = v != "0"
	}
	if v := q.Get("ai"); v != "" {
		ai = v != "0"
	}
	return manual, ai
}

// parseFlowSortQuery reads ?sort=&dir= for History list ordering (server-side).
func parseFlowSortQuery(q url.Values) (key string, dir int) {
	key = store.NormalizeFlowSortKey(q.Get("sort"))
	switch strings.ToLower(q.Get("dir")) {
	case "asc":
		dir = 1
	case "desc":
		dir = -1
	default:
		dir = 0 // store picks a default per key
	}
	return key, dir
}

func (h *Hub) listFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiOr(q.Get("limit"), 200)
	if limit < 1 || limit > 5000 {
		// Guard against a non-positive limit (which would make the truncation
		// reslice `flows[:limit]` panic) and absurd upper bounds.
		limit = 200
	}
	f := store.FlowFilter{
		Limit:        limit + 1, // fetch one extra to detect truncation
		Method:       q.Get("method"),
		Host:         q.Get("host"),
		Search:       q.Get("search"),
		Scheme:       q.Get("scheme"),
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan, // these have their own views
	}
	f.SortKey, f.SortDir = parseFlowSortQuery(q)
	if curID := int64(atoiOr(q.Get("curId"), 0)); curID > 0 {
		f.CursorID = curID
		f.CursorVal = q.Get("curVal")
	} else {
		f.BeforeID = int64(atoiOr(q.Get("before"), 0)) // legacy id-DESC cursor
	}
	showManual, showAI := flowSourceFilters(q)
	if !showManual && !showAI {
		writeJSON(w, http.StatusOK, map[string]any{"flows": []flowJSON{}, "truncated": false})
		return
	}
	if q.Get("discovery") == "1" {
		f.RequireFlags = store.FlagDiscovery
	}
	switch {
	case showManual && showAI && !h.aiDisabled():
		// Both sources: proxy traffic plus AI sends (FlagAI exempts Repeater/Intruder noise).
		f.IncludeFlags = store.FlagAI
	case showAI && !showManual:
		f.RequireFlags |= store.FlagAI
		f.IncludeFlags = store.FlagAI
	case showManual && !showAI:
		f.WithoutFlags = store.FlagAI
	}
	if sc := q.Get("status"); sc != "" {
		f.StatusClass = atoiOr(sc, 0)
	}
	searchScope := strings.ToLower(q.Get("searchScope"))
	var searchNote string
	if searchScope == "body" && strings.TrimSpace(f.Search) != "" {
		ids, note, err := h.st.FlowIDsBodySearch(f, 8000)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		searchNote = note
		f.Search = ""
		f.FlowIDs = ids
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"flows": []flowJSON{}, "truncated": false, "searchNote": note})
			return
		}
	}
	if q.Get("hasNote") == "1" {
		f.HasNote = true
	}
	f.Tag = q.Get("tag")
	// Negative filters (repeatable): notMethod, notHost, notPath, notStatus.
	f.NotMethods, f.NotHosts, f.NotPaths = q["notMethod"], q["notHost"], q["notPath"]
	for _, s := range q["notStatus"] {
		if n := atoiOr(s, 0); n > 0 {
			f.NotStatuses = append(f.NotStatuses, n)
		}
	}
	inScopeOnly := q.Get("inScope") == "1"
	if inScopeOnly {
		want := limit + 1
		matched, more, qerr := h.queryInScopeFlows(f, want)
		if qerr != nil {
			httpErr(w, http.StatusInternalServerError, qerr.Error())
			return
		}
		truncated := more || len(matched) > limit
		if len(matched) > limit {
			matched = matched[:limit]
		}
		_ = h.st.AttachTags(matched)
		out := make([]flowJSON, 0, len(matched))
		for _, fl := range matched {
			out = append(out, toFlowJSON(fl))
		}
		resp := map[string]any{"flows": out, "truncated": truncated}
		if searchNote != "" {
			resp["searchNote"] = searchNote
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	flows, err := h.st.QueryFlowsListFilter(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	truncated := len(flows) > limit
	if truncated {
		flows = flows[:limit]
	}
	_ = h.st.AttachTags(flows)
	out := make([]flowJSON, 0, len(flows))
	for _, fl := range flows {
		out = append(out, toFlowJSON(fl))
	}
	resp := map[string]any{"flows": out, "truncated": truncated}
	if searchNote != "" {
		resp["searchNote"] = searchNote
	}
	writeJSON(w, http.StatusOK, resp)
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
	_ = h.st.AttachTags([]*store.Flow{f})
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

// getFlowBody streams just the (decoded) body bytes with Content-Type and a
// filename extension derived from the MIME type — for downloads when the UI
// won't render the payload inline.
func (h *Hub) getFlowBody(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	side := r.URL.Query().Get("side")
	var mimeType string
	var body []byte
	if side == "res" {
		mimeType = f.Mime
		_, body = decodeForDisplay(f.ResHeaders, h.bodyBytes(f.ResBodyHash))
	} else {
		_, body = decodeForDisplay(f.ReqHeaders, h.bodyBytes(f.ReqBodyHash))
		mimeType = headerContentType(f.ReqHeaders)
	}
	if mimeType == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", mimeType)
	}
	sideLabel := "req"
	if side == "res" {
		sideLabel = "res"
	}
	fn := flowBodyFilename(f.ID, sideLabel, mimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+fn+`"`)
	_, _ = w.Write(body)
}

func headerContentType(hdr map[string][]string) string {
	for k, v := range hdr {
		if strings.EqualFold(k, "content-type") && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func flowBodyFilename(id int64, side, mimeType string) string {
	ext := "bin"
	if mimeType != "" {
		base := strings.TrimSpace(strings.Split(mimeType, ";")[0])
		if exts, _ := mime.ExtensionsByType(base); len(exts) > 0 {
			ext = strings.TrimPrefix(exts[0], ".")
		} else if strings.HasPrefix(base, "text/") {
			sub := strings.TrimPrefix(base, "text/")
			if sub != "" && sub != "plain" {
				ext = sub
			} else {
				ext = "txt"
			}
		}
	}
	return fmt.Sprintf("flow-%d-%s.%s", id, side, ext)
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
// maxRulePattern bounds a user-supplied match/intercept regex. Go's RE2 is linear
// (no catastrophic ReDoS), but a very long pattern run against large bodies on
// every proxied request is still costly; real patterns are short.
const maxRulePattern = 4096

func validRule(w http.ResponseWriter, in ruleJSON) bool {
	switch in.Type {
	case "req-header", "req-body", "res-header", "res-body":
	default:
		httpErr(w, http.StatusBadRequest, "type must be req-header, req-body, res-header, or res-body")
		return false
	}
	if len(in.Match) > maxRulePattern {
		httpErr(w, http.StatusBadRequest, "match pattern too long")
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

func heldFromRequest(held intercept.Held, includeRaw bool) heldJSON {
	hj := heldJSON{ID: held.ID, Len: len(held.Raw)}
	if held.Flow != nil {
		hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
	}
	if includeRaw {
		hj.Raw = string(held.Raw)
	}
	return hj
}

func heldFromResponse(held intercept.HeldResponse, includeRaw bool) heldJSON {
	hj := heldJSON{ID: held.ID, Len: len(held.Raw)}
	if held.Flow != nil {
		hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
	}
	if includeRaw {
		hj.Raw = string(held.Raw)
	}
	return hj
}

func (h *Hub) interceptState() interceptJSON {
	return h.interceptStateWithRaw(true)
}

func (h *Hub) interceptStateSummary() interceptJSON {
	return h.interceptStateWithRaw(false)
}

func (h *Hub) interceptStateWithRaw(includeRaw bool) interceptJSON {
	out := interceptJSON{}
	if h.eng == nil {
		return out
	}
	out.Enabled = h.eng.Enabled()
	for _, held := range h.eng.Queue() {
		out.Queue = append(out.Queue, heldFromRequest(held, includeRaw))
	}
	out.ResponseEnabled = h.eng.ResponseEnabled()
	for _, held := range h.eng.ResponseQueue() {
		out.ResponseQueue = append(out.ResponseQueue, heldFromResponse(held, includeRaw))
	}
	out.FilterEnabled, out.FilterTarget, out.FilterPattern = h.eng.InterceptFilter()
	return out
}

func (h *Hub) getInterceptHeldRaw(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	side := r.URL.Query().Get("side")
	if side == "resp" {
		for _, held := range h.eng.ResponseQueue() {
			if held.ID == id {
				writeJSON(w, http.StatusOK, map[string]any{"raw": string(held.Raw)})
				return
			}
		}
	} else {
		for _, held := range h.eng.Queue() {
			if held.ID == id {
				writeJSON(w, http.StatusOK, map[string]any{"raw": string(held.Raw)})
				return
			}
		}
	}
	httpErr(w, http.StatusNotFound, "not held")
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if len(in.Pattern) > maxRulePattern {
		httpErr(w, http.StatusBadRequest, "pattern too long")
		return
	}
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
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
	aiDisabled, _, _ := h.st.GetSetting("ai.disabled")
	writeJSON(w, http.StatusOK, settingsJSON{
		ProxyAddr:                h.currentProxyAddr(),
		InterceptEnabled:         h.eng != nil && h.eng.Enabled(),
		UpstreamProxy:            up,
		AiProvider:               aiProvider,
		AiModel:                  aiModel,
		AiHasKey:                 !h.aiDisabled() && (aiKey != "" || envKey != ""),
		AiDisabled:               aiDisabled == "1",
		OobEnabled:               h.oobEnabled(),
		CaptureScopeOnly:         scopeOnly == "1",
		SuppressBrowserTelemetry: suppressTelemetryOn,
	})
}

func (h *Hub) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProxyAddr                string  `json:"proxyAddr"`
		UpstreamProxy            *string `json:"upstreamProxy"` // pointer so "" can clear it
		AiProvider               *string `json:"aiProvider"`
		AiApiKey                 *string `json:"aiApiKey"`
		AiModel                  *string `json:"aiModel"`
		AiDisabled               *bool   `json:"aiDisabled"`
		OobEnabled               *bool   `json:"oobEnabled"`
		CaptureScopeOnly         *bool   `json:"captureScopeOnly"`
		SuppressBrowserTelemetry *bool   `json:"suppressBrowserTelemetry"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.AiProvider != nil || in.AiApiKey != nil || in.AiModel != nil {
		if h.aiDisabled() && (in.AiDisabled == nil || *in.AiDisabled) {
			httpErr(w, http.StatusForbidden, aiDisabledMsg)
			return
		}
		prov, _, _ := h.st.GetSetting("ai.provider")
		if prov == "" {
			prov = aiassist.ProviderAnthropic
		}
		if in.AiProvider != nil {
			prov = *in.AiProvider
		}
		if prov == aiassist.ProviderOpenRouter {
			key, _, _ := h.st.GetSetting("ai.apiKey")
			if in.AiApiKey != nil {
				key = *in.AiApiKey
			}
			if key == "" {
				key = os.Getenv("OPENROUTER_API_KEY")
			}
			model, _, _ := h.st.GetSetting("ai.model")
			if in.AiModel != nil {
				model = *in.AiModel
			}
			if err := aiassist.ValidateOpenRouter(r.Context(), key, model); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
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
	if in.AiDisabled != nil {
		v := "0"
		if *in.AiDisabled {
			v = "1"
		}
		_ = h.st.SetSetting("ai.disabled", v)
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.OobEnabled != nil {
		v := "0"
		if *in.OobEnabled {
			v = "1"
		}
		_ = h.st.SetSetting("oob.enabled", v)
		h.broadcast(map[string]any{"type": "settings.update"})
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
