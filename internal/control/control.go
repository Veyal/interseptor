// Package control serves the Interseptor UI and the REST + SSE control API on
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
	"log"
	"mime"
	"net"
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

	"github.com/Veyal/interseptor/internal/aiassist"
	"github.com/Veyal/interseptor/internal/autopwn"
	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/strutil"
	"github.com/Veyal/interseptor/internal/tlsca"
	"github.com/Veyal/interseptor/internal/tunnel"
)

//go:embed ui
var uiFS embed.FS

// Rebinder lets the control plane move a listener at runtime.
type Rebinder interface {
	Rebind(addr string) error // open the new listener first; keep the old one on failure
	Addr() string             // the current listen address (comma-joined when multiple)
}

// MultiProxyRebinder supports multiple proxy listeners sharing one handler.
type MultiProxyRebinder interface {
	RebindAddrs(addrs []string) error
	Addrs() []string
}

// Hub is the control-plane HTTP handler and live-event broadcaster. It also
// implements the proxy's Events sink (FlowCaptured).
type Hub struct {
	st         *store.Store
	eng        *intercept.Engine
	ca         *tlsca.CA
	rebind     Rebinder // proxy listener
	ctrlRebind Rebinder // control UI/API listener
	// SyncSelfPorts updates proxy SelfPorts and hub control address after a listener rebind.
	SyncSelfPorts func()
	snd           *sender.Sender
	intr          *intruder.Engine
	sc            *scope.Engine
	oob           *oob.Catcher
	hi            *humanInput // pending AI→human input prompts (request_human_input)
	mux           *http.ServeMux

	// Upstream applies a chained upstream-proxy URL ("" = direct). Set by cmd.
	Upstream func(string) error
	// SetCaptureScopeOnly toggles persisting only in-scope traffic. Set by cmd.
	SetCaptureScopeOnly func(bool)
	// SetSuppressBrowserTelemetry toggles suppression of Chrome/Firefox telemetry. Set by cmd.
	SetSuppressBrowserTelemetry func(bool)
	// SetSuppressAndroidTelemetry toggles suppression of Android/GMS/Crashlytics telemetry. Set by cmd.
	SetSuppressAndroidTelemetry func(bool)
	// SetInvisibleProxy toggles transparent/invisible proxy mode. Set by cmd.
	SetInvisibleProxy func(bool)
	// SetTLSBypassHosts replaces the list of hosts tunneled raw (no MITM). Set by cmd.
	SetTLSBypassHosts func([]string)
	// SetAutoBypassOnPinFailure toggles auto-adding a host to the bypass list on
	// an MITM handshake failure (SSL pinning). Set by cmd.
	SetAutoBypassOnPinFailure func(bool)

	// ChecksDir holds user-authored Starlark scanner checks (global, shared across
	// projects — typically ~/.interseptor/checks). Set by cmd.
	ChecksDir string

	// ActiveChecksDir holds user-authored Starlark ACTIVE checks (global, shared —
	// typically ~/.interseptor/active-checks). Set by cmd.
	ActiveChecksDir string

	// selfAddr is this control plane's own host:port (e.g. 127.0.0.1:9966). Set by
	// cmd; the active scanner refuses to target it, so it never attacks its own API.
	selfAddr atomic.Pointer[string]

	// ProjectName/ProjectDir identify the active project (Burp-style). Set by cmd;
	// surfaced at GET /api/version so the UI can show which project is loaded.
	ProjectName string
	ProjectDir  string
	// GlobalDir is ~/.interseptor (named projects live in GlobalDir/projects).
	// SwitchProject re-launches Interseptor into another project; nil if unsupported.
	GlobalDir     string
	SwitchProject func(target string) error

	switchMu    sync.Mutex  // guards switchTimer
	switchTimer *time.Timer // pending delayed project switch; reset per request so only the latest fires

	mcpMu       sync.Mutex
	mcpSrv      *mcp.Server // lazily built streamable-HTTP MCP front end (POST /mcp)
	mcpKeysSeen atomic.Bool // last-known "API keys exist" — mcpAuthorized fails closed on a store error once true

	tun             tunnelManager // Cloudflare quick-tunnel manager (remote sharing)
	tunnelCloseOnce sync.Once

	as asState // active-scan state (armed/running/findings)

	autopwnMu     sync.Mutex      // guards lazy engine + tool-bus construction
	autopwnEngine *autopwn.Engine // autonomous-pentest ("Autopilot") run engine (built lazily)
	autopwnTools  *mcp.Server     // in-process tool bus (built lazily once the control addr is known)

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
	h.tun = tunnel.New(func() string {
		addr := h.currentControlAddr()
		if _, p, err := net.SplitHostPort(addr); err == nil && p != "" {
			return p
		}
		return "9966"
	})
	h.tun.SetOnURL(func(url string) {
		h.broadcast(map[string]any{"type": "tunnel.update", "url": url})
	})
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

// SetControlRebinder attaches the control-plane listener manager (set by cmd).
func (h *Hub) SetControlRebinder(r Rebinder) { h.ctrlRebind = r }

// Handler returns the control-plane HTTP handler, wrapped in the loopback/CSRF
// security guard (see securityGuard).
func (h *Hub) Handler() http.Handler { return h.securityGuard(h.mux) }

// handleMCP serves the Streamable-HTTP MCP transport. The backing mcp.Server is
// built against this control server's own LOOPBACK address — not the request Host
// — so its tool calls always loop back locally even when the client reached us
// over a tunnel (a tunnel Host would send the tool bus back out over the internet,
// where the auth gate would then 401 the unauthenticated internal calls).
func (h *Hub) handleMCP(w http.ResponseWriter, r *http.Request) {
	h.mcpMu.Lock()
	if h.mcpSrv == nil {
		h.mcpSrv = mcp.New(h.loopbackControlBase())
	}
	srv := h.mcpSrv
	h.mcpMu.Unlock()
	srv.ServeHTTP(w, r)
}

// loopbackControlBase returns an http://127.0.0.1:<port> base URL for the control
// plane, derived from its current listen address (which may be 0.0.0.0). Used to
// keep the in-process MCP tool bus on loopback regardless of external exposure.
func (h *Hub) loopbackControlBase() string {
	addr := h.currentControlAddr()
	port := "9966"
	if _, p, err := net.SplitHostPort(addr); err == nil && p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port
}

// ---- DTOs ----

type flowJSON struct {
	ID         int64    `json:"id"`
	TS         int64    `json:"ts"`
	Method     string   `json:"method"`
	Scheme     string   `json:"scheme"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Path       string   `json:"path"`
	Status     int      `json:"status"`
	Mime       string   `json:"mime"`
	ReqLen     int64    `json:"reqLen"`
	ResLen     int64    `json:"resLen"`
	DurationMs int64    `json:"durationMs"`
	ClientAddr string   `json:"clientAddr"`
	Error      string   `json:"error"`
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
	ProxyAddr                string   `json:"proxyAddr"`
	ProxyAddrs               []string `json:"proxyAddrs,omitempty"`
	ControlAddr              string   `json:"controlAddr"`
	InterceptEnabled         bool     `json:"interceptEnabled"`
	UpstreamProxy            string   `json:"upstreamProxy"`
	AiProvider               string   `json:"aiProvider"`
	AiModel                  string   `json:"aiModel"`
	AiEndpoint               string   `json:"aiEndpoint"`
	AiHasKey                 bool     `json:"aiHasKey"` // never returns the key itself
	AiDisabled               bool     `json:"aiDisabled"`
	OobEnabled               bool     `json:"oobEnabled"`
	CaptureScopeOnly         bool     `json:"captureScopeOnly"`
	SuppressBrowserTelemetry bool     `json:"suppressBrowserTelemetry"`
	SuppressAndroidTelemetry bool     `json:"suppressAndroidTelemetry"`
	InvisibleProxy           bool     `json:"invisibleProxy"`
	TLSBypassHosts           []string `json:"tlsBypassHosts"`
	AutoBypassOnPinFailure   bool     `json:"autoBypassOnPinFailure"`
	DeviceProxy              string   `json:"deviceProxy,omitempty"`
	DeviceProxyMode          string   `json:"deviceProxyMode,omitempty"`
}

// tlsBypassSettingKey stores the newline-separated host patterns that bypass MITM.
const tlsBypassSettingKey = "proxy.tlsBypassHosts"

// parseHostList splits a stored/edited host-list blob (newline- or comma-
// separated) into trimmed, non-empty, lower-cased, de-duplicated patterns.
func parseHostList(s string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		h := strings.ToLower(strings.TrimSpace(part))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
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
func (h *flowAPI) setFlowNote(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
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
func (h *flowAPI) broadcastFlowTags(id int64) {
	if f, err := h.st.GetFlow(id); err == nil {
		_ = h.st.AttachTags([]*store.Flow{f})
		h.FlowUpdated(f)
	}
}

// setFlowTags replaces a flow's tag set ({"tags":[...]}). Used by the inspector and
// the right-click "Tag…" action.
func (h *flowAPI) setFlowTags(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
		return
	}
	h.broadcastFlowTags(id)
	h.broadcast(map[string]any{"type": "tags.update"})
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// addFlowTagsBulk adds or removes tags on many flows at once
// ({"flowIds":[...],"add":[...],"remove":[...]}) — for tagging a multi-selection from History.
func (h *flowAPI) addFlowTagsBulk(w http.ResponseWriter, r *http.Request) {
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
				httpInternalErr(w, err)
				return
			}
		}
		for _, t := range store.NormalizeTags(in.Remove) {
			if err := h.st.RemoveFlowTag(id, t); err != nil {
				httpInternalErr(w, err)
				return
			}
		}
		h.broadcastFlowTags(id)
	}
	h.broadcast(map[string]any{"type": "tags.update"})
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.FlowIDs)})
}

// listTags returns every tag in use with flow counts and colors.
func (h *flowAPI) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.st.DistinctTags()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// setTagColor sets (or clears) a tag's display color ({"color":"#rrggbb"}).
func (h *flowAPI) setTagColor(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
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

func (h *flowAPI) deleteFlows(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
		return
	}
	if n > 0 {
		h.epsCache.invalidate()
		h.broadcast(map[string]any{"type": "flow.new"}) // reuse the reload signal
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// maxEndpointList caps the attack-surface map payload so the UI stays responsive
// on large projects (tens of thousands of unique paths).
const maxEndpointList = 12000

// listEndpoints returns the unique-endpoint map for the attack-surface view —
// proxied/manual traffic aggregated by (host, method, path); bulk attack traffic
// (Intruder / active scan) is excluded as noise.
func (h *flowAPI) listEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EndpointFilter{
		Host:          q.Get("host"),
		Search:        q.Get("search"),
		SearchScope:   q.Get("searchScope"),
		ExcludeFlags:  store.FlagIntruder | store.FlagActiveScan,
		Tag:           q.Get("tag"),
		HideNoiseOnly: q.Get("hideNoise") != "0", // on by default — ferox/discovery 403/404-only paths
	}
	key := endpointsCacheKey(f)
	if eps, note, total, truncated, ok := h.epsCache.get(key); ok {
		out := map[string]any{"endpoints": eps, "total": total}
		if truncated {
			out["truncated"] = true
		}
		if note != "" {
			out["searchNote"] = note
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	eps, note, err := h.st.Endpoints(f)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if eps == nil {
		eps = []store.Endpoint{}
	}
	total := len(eps)
	truncated := false
	if total > maxEndpointList {
		eps = eps[:maxEndpointList]
		truncated = true
	}
	h.epsCache.set(key, eps, note, total, truncated)
	out := map[string]any{"endpoints": eps, "total": total}
	if truncated {
		out["truncated"] = true
	}
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

func (h *flowAPI) listFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiOr(q.Get("limit"), 200)
	if limit < 1 || limit > 5000 {
		// Guard against a non-positive limit (which would make the truncation
		// reslice `flows[:limit]` panic) and absurd upper bounds.
		limit = 200
	}
	f := store.FlowFilter{
		Limit:  limit + 1, // fetch one extra to detect truncation
		Method: q.Get("method"),
		Host:   q.Get("host"),
		Search: q.Get("search"),
		Scheme: q.Get("scheme"),
	}
	// History hides Repeater/Intruder/ActiveScan by default (they have their own
	// views). includeTools=1 is the escape hatch for MCP agents / triage that
	// need to see tool-generated traffic alongside proxy captures.
	if q.Get("includeTools") != "1" {
		f.ExcludeFlags = store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan
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
	if q.Get("tlsFailed") == "1" {
		f.RequireFlags |= store.FlagTLSFailed
	} else if q.Get("hideTlsFailed") == "1" {
		f.WithoutFlags |= store.FlagTLSFailed
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
	f.SearchScope = strings.ToLower(strings.TrimSpace(q.Get("searchScope")))
	searchScope := f.SearchScope
	var searchNote string
	if searchScope == "body" && strings.TrimSpace(f.Search) != "" {
		ids, note, err := h.st.FlowIDsBodySearch(f, 8000)
		if err != nil {
			httpInternalErr(w, err)
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
			httpInternalErr(w, qerr)
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
		httpInternalErr(w, err)
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

func (h *flowAPI) getFlow(w http.ResponseWriter, r *http.Request) {
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

func (h *flowAPI) getFlowRaw(w http.ResponseWriter, r *http.Request) {
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
func (h *flowAPI) getFlowBody(w http.ResponseWriter, r *http.Request) {
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

func (h *flowAPI) flowWS(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	frames, err := h.st.QueryWSFrames(id, 2000)
	if err != nil {
		httpInternalErr(w, err)
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

func (h *flowAPI) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.st.ListRules()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	out := make([]ruleJSON, 0, len(rules))
	for _, ru := range rules {
		out = append(out, ruleJSON(ru))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func (h *flowAPI) createRule(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
		return
	}
	in.ID = id
	h.refreshRules()
	writeJSON(w, http.StatusCreated, in)
}

func (h *flowAPI) updateRule(w http.ResponseWriter, r *http.Request) {
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
		httpInternalErr(w, err)
		return
	}
	h.refreshRules()
	writeJSON(w, http.StatusOK, in)
}

func (h *flowAPI) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteRule(id); err != nil {
		httpInternalErr(w, err)
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

func (h *interceptAPI) getInterceptHeldRaw(w http.ResponseWriter, r *http.Request) {
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

func (h *interceptAPI) getIntercept(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.interceptState())
}

func (h *interceptAPI) toggleIntercept(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, h.interceptState())
}

// setInterceptFilter configures the conditional-intercept regex filter and
// persists it so the choice survives restarts.
func (h *interceptAPI) setInterceptFilter(w http.ResponseWriter, r *http.Request) {
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
	if !h.persistSetting(w, "intercept.filter.enabled", boolToFlag(enabled)) {
		return
	}
	if !h.persistSetting(w, "intercept.filter.target", target) {
		return
	}
	if !h.persistSetting(w, "intercept.filter.pattern", pattern) {
		return
	}
	writeJSON(w, http.StatusOK, h.interceptState())
}

func (h *interceptAPI) forwardIntercept(w http.ResponseWriter, r *http.Request) {
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

func (h *interceptAPI) dropIntercept(w http.ResponseWriter, r *http.Request) {
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

func (h *interceptAPI) toggleResponseIntercept(w http.ResponseWriter, r *http.Request) {
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

func (h *interceptAPI) forwardResponse(w http.ResponseWriter, r *http.Request) {
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

func (h *interceptAPI) dropResponse(w http.ResponseWriter, r *http.Request) {
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

func (h *Hub) currentProxyAddrs() []string {
	if mr, ok := h.rebind.(MultiProxyRebinder); ok {
		if addrs := mr.Addrs(); len(addrs) > 0 {
			return addrs
		}
	}
	if h.rebind != nil {
		if a := h.rebind.Addr(); a != "" {
			if strings.Contains(a, ", ") {
				return parseProxyAddrsRaw(strings.ReplaceAll(a, ", ", "\n"))
			}
			return []string{a}
		}
	}
	return loadProxyAddrs(h.st)
}

func (h *Hub) currentProxyAddr() string {
	return displayProxyAddrs(h.currentProxyAddrs())
}

func (h *Hub) hasExternalProxyOnPort(port int) bool {
	for _, addr := range h.currentProxyAddrs() {
		_, p := proxyHostPort(addr)
		if p == port && isExternalProxyBind(addr) {
			return true
		}
	}
	return false
}

func (h *Hub) SetSelfAddr(addr string) {
	h.selfAddr.Store(&addr)
}

func (h *Hub) GetSelfAddr() string {
	if p := h.selfAddr.Load(); p != nil {
		return *p
	}
	return ""
}

func (h *Hub) currentControlAddr() string {
	if h.ctrlRebind != nil {
		return h.ctrlRebind.Addr()
	}
	if addr := h.GetSelfAddr(); addr != "" {
		return addr
	}
	if v, ok, _ := h.st.GetSetting("control.addr"); ok && v != "" {
		return v
	}
	return "127.0.0.1:9966"
}

func (h *settingsAPI) getSettings(w http.ResponseWriter, r *http.Request) {
	up, _, _ := h.st.GetSetting("upstream.proxy")
	aiProvider, _, _ := h.st.GetSetting("ai.provider")
	if aiProvider == "" {
		aiProvider = "anthropic"
	}
	aiKey, _, _ := h.st.GetSetting("ai.apiKey")
	aiModel, _, _ := h.st.GetSetting("ai.model")
	aiEndpoint, _, _ := h.st.GetSetting("ai.endpoint")
	envKey := os.Getenv("ANTHROPIC_API_KEY")
	switch aiProvider {
	case "openrouter":
		envKey = os.Getenv("OPENROUTER_API_KEY")
	case aiassist.ProviderGLM:
		if envKey = os.Getenv("GLM_API_KEY"); envKey == "" {
			envKey = os.Getenv("ZAI_API_KEY")
		}
	}
	scopeOnly, _, _ := h.st.GetSetting("capture.scopeOnly")
	suppressTelemetry, stOK, _ := h.st.GetSetting("capture.suppressBrowserTelemetry")
	// Default to true when the key has never been written (first run).
	suppressTelemetryOn := !stOK || suppressTelemetry == "1"
	suppressAndroid, andOK, _ := h.st.GetSetting("capture.suppressAndroidTelemetry")
	suppressAndroidOn := !andOK || suppressAndroid == "1"
	invisibleProxy, _, _ := h.st.GetSetting("proxy.invisibleProxy")
	tlsBypassRaw, _, _ := h.st.GetSetting(tlsBypassSettingKey)
	autoBypass, _, _ := h.st.GetSetting("proxy.autoBypassOnPinFailure")
	aiDisabled, _, _ := h.st.GetSetting("ai.disabled")
	proxyAddrs := h.currentProxyAddrs()
	deviceEP := h.resolveDeviceEndpoint()
	writeJSON(w, http.StatusOK, settingsJSON{
		ProxyAddr:                displayProxyAddrs(proxyAddrs),
		ProxyAddrs:               proxyAddrs,
		DeviceProxy:              deviceEP.Endpoint,
		DeviceProxyMode:          loadDeviceProxyMode(h.st),
		ControlAddr:              h.currentControlAddr(),
		InterceptEnabled:         h.eng != nil && h.eng.Enabled(),
		UpstreamProxy:            up,
		AiProvider:               aiProvider,
		AiModel:                  aiModel,
		AiEndpoint:               aiEndpoint,
		AiHasKey:                 !h.aiDisabled() && (aiKey != "" || envKey != ""),
		AiDisabled:               aiDisabled == "1",
		OobEnabled:               h.oobEnabled(),
		CaptureScopeOnly:         scopeOnly == "1",
		SuppressBrowserTelemetry: suppressTelemetryOn,
		SuppressAndroidTelemetry: suppressAndroidOn,
		InvisibleProxy:           invisibleProxy == "1",
		TLSBypassHosts:           parseHostList(tlsBypassRaw),
		AutoBypassOnPinFailure:   autoBypass == "1",
	})
}

// NotifyBypassAdded persists an updated TLS-bypass host list (produced when the
// proxy auto-bypasses a pinned host) and pushes a settings refresh to the UI.
// Wired as the proxy's OnBypassAdded callback; safe to call from proxy goroutines.
func (h *Hub) NotifyBypassAdded(hosts []string) {
	if err := h.st.SetSetting(tlsBypassSettingKey, strings.Join(hosts, "\n")); err != nil {
		log.Printf("control: persist auto-bypass host list: %v", err)
	}
	h.broadcast(map[string]any{"type": "settings.update"})
}

func (h *Hub) persistSetting(w http.ResponseWriter, key, val string) bool {
	if err := h.st.SetSetting(key, val); err != nil {
		httpInternalErr(w, err)
		return false
	}
	return true
}

func (h *settingsAPI) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProxyAddr                string    `json:"proxyAddr"`
		ProxyAddrs               []string  `json:"proxyAddrs"`
		ControlAddr              string    `json:"controlAddr"`
		UpstreamProxy            *string   `json:"upstreamProxy"` // pointer so "" can clear it
		AiProvider               *string   `json:"aiProvider"`
		AiApiKey                 *string   `json:"aiApiKey"`
		AiModel                  *string   `json:"aiModel"`
		AiEndpoint               *string   `json:"aiEndpoint"`
		AiDisabled               *bool     `json:"aiDisabled"`
		OobEnabled               *bool     `json:"oobEnabled"`
		CaptureScopeOnly         *bool     `json:"captureScopeOnly"`
		SuppressBrowserTelemetry *bool     `json:"suppressBrowserTelemetry"`
		SuppressAndroidTelemetry *bool     `json:"suppressAndroidTelemetry"`
		InvisibleProxy           *bool     `json:"invisibleProxy"`
		TLSBypassHosts           *[]string `json:"tlsBypassHosts"`
		AutoBypassOnPinFailure   *bool     `json:"autoBypassOnPinFailure"`
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
		if !h.persistSetting(w, "ai.provider", *in.AiProvider) {
			return
		}
	}
	if in.AiApiKey != nil {
		if !h.persistSetting(w, "ai.apiKey", *in.AiApiKey) {
			return
		}
	}
	if in.AiModel != nil {
		if !h.persistSetting(w, "ai.model", *in.AiModel) {
			return
		}
	}
	if in.AiEndpoint != nil {
		if !h.persistSetting(w, "ai.endpoint", *in.AiEndpoint) {
			return
		}
	}
	if in.AiDisabled != nil {
		v := "0"
		if *in.AiDisabled {
			v = "1"
		}
		if !h.persistSetting(w, "ai.disabled", v) {
			return
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.OobEnabled != nil {
		v := "0"
		if *in.OobEnabled {
			v = "1"
		}
		if !h.persistSetting(w, "oob.enabled", v) {
			return
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.CaptureScopeOnly != nil {
		v := "0"
		if *in.CaptureScopeOnly {
			v = "1"
		}
		if !h.persistSetting(w, "capture.scopeOnly", v) {
			return
		}
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
		if !h.persistSetting(w, "capture.suppressBrowserTelemetry", v) {
			return
		}
		if h.SetSuppressBrowserTelemetry != nil {
			h.SetSuppressBrowserTelemetry(*in.SuppressBrowserTelemetry)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.SuppressAndroidTelemetry != nil {
		v := "0"
		if *in.SuppressAndroidTelemetry {
			v = "1"
		}
		if !h.persistSetting(w, "capture.suppressAndroidTelemetry", v) {
			return
		}
		if h.SetSuppressAndroidTelemetry != nil {
			h.SetSuppressAndroidTelemetry(*in.SuppressAndroidTelemetry)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.InvisibleProxy != nil {
		v := "0"
		if *in.InvisibleProxy {
			v = "1"
		}
		if !h.persistSetting(w, "proxy.invisibleProxy", v) {
			return
		}
		if h.SetInvisibleProxy != nil {
			h.SetInvisibleProxy(*in.InvisibleProxy)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.TLSBypassHosts != nil {
		hosts := parseHostList(strings.Join(*in.TLSBypassHosts, "\n"))
		if !h.persistSetting(w, tlsBypassSettingKey, strings.Join(hosts, "\n")) {
			return
		}
		if h.SetTLSBypassHosts != nil {
			h.SetTLSBypassHosts(hosts)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.AutoBypassOnPinFailure != nil {
		v := "0"
		if *in.AutoBypassOnPinFailure {
			v = "1"
		}
		if !h.persistSetting(w, "proxy.autoBypassOnPinFailure", v) {
			return
		}
		if h.SetAutoBypassOnPinFailure != nil {
			h.SetAutoBypassOnPinFailure(*in.AutoBypassOnPinFailure)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	newProxyAddrs := in.ProxyAddrs
	if len(newProxyAddrs) == 0 && in.ProxyAddr != "" {
		newProxyAddrs = []string{in.ProxyAddr}
	}
	if len(newProxyAddrs) > 0 && !proxyAddrsEqual(newProxyAddrs, h.currentProxyAddrs()) {
		newProxyAddrs = normalizeProxyAddrs(newProxyAddrs)
		if err := validateProxyAddrs(newProxyAddrs); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if mr, ok := h.rebind.(MultiProxyRebinder); ok {
			if err := mr.RebindAddrs(newProxyAddrs); err != nil {
				httpErr(w, http.StatusBadRequest, "rebind failed: "+err.Error())
				return
			}
		} else if h.rebind != nil && len(newProxyAddrs) == 1 {
			if err := h.rebind.Rebind(newProxyAddrs[0]); err != nil {
				httpErr(w, http.StatusBadRequest, "rebind failed: "+err.Error())
				return
			}
		} else if len(newProxyAddrs) > 1 {
			httpErr(w, http.StatusBadRequest, "multiple proxy listeners are not supported by this build")
			return
		}
		if !h.persistSetting(w, "proxy.addrs", formatProxyAddrs(newProxyAddrs)) {
			return
		}
		if !h.persistSetting(w, "proxy.addr", newProxyAddrs[0]) {
			return
		}
		if h.SyncSelfPorts != nil {
			h.SyncSelfPorts()
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.ControlAddr != "" && in.ControlAddr != h.currentControlAddr() {
		if !isLoopbackHost(in.ControlAddr) && !bind.ExternalBindAllowed() {
			httpErr(w, http.StatusBadRequest, "control bind address must be loopback (127.0.0.1/localhost/::1); external bind is disabled (INTERSEPTOR_ALLOW_EXTERNAL_BIND=0)")
			return
		}
		if h.ctrlRebind != nil {
			if err := h.ctrlRebind.Rebind(in.ControlAddr); err != nil {
				httpErr(w, http.StatusBadRequest, "control rebind failed: "+err.Error())
				return
			}
		}
		h.SetSelfAddr(in.ControlAddr)
		if !h.persistSetting(w, "control.addr", in.ControlAddr) {
			return
		}
		if h.SyncSelfPorts != nil {
			h.SyncSelfPorts()
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if in.UpstreamProxy != nil {
		if h.Upstream != nil {
			if err := h.Upstream(*in.UpstreamProxy); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if !h.persistSetting(w, "upstream.proxy", *in.UpstreamProxy) {
			return
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	h.getSettings(w, r)
}

func (h *settingsAPI) getCA(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="interseptor-ca.crt"`)
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

// httpInternalErr logs the real error server-side and returns a scrubbed 500 so
// Go/SQLite internals (e.g. "database is locked") aren't leaked to clients. Use
// for unexpected 5xx; keep httpErr for caller-authored, client-safe messages.
func httpInternalErr(w http.ResponseWriter, err error) {
	log.Printf("control: 500: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
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

func atoiOr(s string, def int) int { return strutil.AtoiOr(s, def) }

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
