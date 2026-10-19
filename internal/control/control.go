// Package control serves the Interseptor UI and the REST + SSE control API on
// the fixed localhost control port. It bridges the browser UI to the store and
// the intercept engine and pushes live events (captured flows, hold-queue
// changes) over Server-Sent Events.
package control

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/ios"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/scriptworker"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/strutil"
	"github.com/Veyal/interseptor/internal/sysproxy"
	"github.com/Veyal/interseptor/internal/testbrowser"
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
	authzMu       sync.Mutex // serializes read-modify-write of saved authz identities

	// Upstream applies a chained upstream-proxy URL ("" = direct). Set by cmd.
	Upstream func(string) error
	// SetUpstreamProxyCA applies PEM trust roots for the chained upstream proxy. Set by cmd.
	SetUpstreamProxyCA func([]byte) error
	setSetting         func(string, string) error
	setSettings        func(map[string]string) error
	sysProxyEnable     func(string, int) error
	sysProxyDisable    func() error
	sysProxyStatus     func() (bool, error)
	sysProxySupported  func() bool
	// launchTestBrowser starts an isolated Chrome/Chromium pointed at the proxy;
	// testBrowserAvailable reports whether a browser could be found. Seams so the
	// handler is testable without launching a real browser.
	launchTestBrowser    func(proxy string) (testbrowser.LaunchResult, error)
	testBrowserAvailable func() bool
	// SetCaptureScopeOnly toggles persisting only in-scope traffic. Set by cmd.
	SetCaptureScopeOnly func(bool)
	// SetSuppressBrowserTelemetry toggles suppression of known Chrome/Firefox
	// background-service traffic. The name is retained for API compatibility.
	SetSuppressBrowserTelemetry func(bool)
	// SetSuppressAndroidTelemetry toggles suppression of Android/GMS/Crashlytics telemetry. Set by cmd.
	SetSuppressAndroidTelemetry func(bool)
	// SetInvisibleProxy toggles transparent/invisible proxy mode. Set by cmd.
	SetInvisibleProxy func(bool)
	// SetProxyBasicAuth replaces the optional proxy-listener username and
	// password. enabled false is the default and accepts every client. Set by cmd.
	SetProxyBasicAuth             func(enabled bool, user, password string)
	SetTLSBypassHosts             func([]string)
	SetOriginTLSVerify            func(bool)
	SetOriginTLSVerifyBypassHosts func([]string)
	// SetAutoBypassOnPinFailure toggles auto-adding a host to the bypass list on
	// an MITM handshake failure (SSL pinning). Set by cmd.
	SetAutoBypassOnPinFailure func(bool)

	// ScriptRouter picks the engine for collection scripts: the out-of-process
	// worker, or in-process for trusted scripts when InProcessTrusted is set.
	// The zero value runs everything in-process (tests); cmd wires the
	// worker. Set before serving.
	ScriptRouter scriptworker.Router
	collAPI      *collectionsAPI

	// ChecksDir holds user-authored Starlark scanner checks (global, shared across
	// projects — typically ~/.interseptor/checks). Set by cmd.
	ChecksDir string

	// selfAddr is this control plane's own host:port (e.g. 127.0.0.1:9966). Set by
	// cmd; request-generating tools refuse to target it.
	selfAddr atomic.Pointer[string]

	// ProjectName/ProjectDir identify the active project (Burp-style). Set by cmd;
	// surfaced at GET /api/version so the UI can show which project is loaded.
	ProjectName string
	ProjectDir  string
	// GlobalDir is ~/.interseptor (named projects live in GlobalDir/projects).
	// SwitchProject re-launches Interseptor into another project; nil if unsupported.
	GlobalDir     string
	SwitchProject func(target string) error

	switchMu         sync.Mutex  // guards the delayed project-switch lifecycle
	switchTimer      *time.Timer // pending delayed project switch; reset per request so only the latest fires
	projectOpenNoted atomic.Bool // first project listing in this process records last opened
	switchClosed     bool
	switchWG         sync.WaitGroup

	mcpMu            sync.Mutex
	mcpSrv           *mcp.Server // lazily built streamable-HTTP MCP front end (POST /mcp)
	scanJob          scanJob     // running passive scans, so POST /api/scanner/cancel can stop one
	beforeScanCommit func()      // test seam fired in the scan's commit window; nil in production, set before serving
	mcpKeysSeen      atomic.Bool // last-known durable auth mode — mcpAuthorized fails closed on a store error once true
	iosTCPReachable  func(string, int) bool

	tun             tunnelManager // Cloudflare quick-tunnel manager (remote sharing)
	tunnelCloseOnce sync.Once

	updMu     sync.Mutex // update-check result (set by cmd's background check)
	updLatest string
	updAvail  bool

	mu      sync.Mutex
	clients map[chan string]struct{}
	// streamStop ends every long-lived event stream (CloseStreams) so a
	// graceful server shutdown is not held open by the UI's /api/events.
	streamStop     chan struct{}
	streamStopOnce sync.Once

	epsCache endpointsCache

	wsMu     sync.Mutex
	wsTimers map[int64]*time.Timer // debounce ws.frame SSE per flow

	activitySocket      net.Listener
	activitySocketPath  string
	activitySocketToken string
	activitySocketWG    sync.WaitGroup

	maintenanceMu     sync.Mutex
	maintenanceClosed bool
	maintenanceWG     sync.WaitGroup
	gcBodiesFn        func() (int64, int64, error)
}

// New builds a Hub. eng, ca, and rebind may be nil. If eng is non-nil, the
// hub registers itself as the intercept change notifier.
func New(st *store.Store, eng *intercept.Engine, ca *tlsca.CA, rebind Rebinder, sc *scope.Engine) *Hub {
	if sc == nil {
		sc = scope.New()
	}
	h := &Hub{
		st:                   st,
		eng:                  eng,
		ca:                   ca,
		rebind:               rebind,
		sc:                   sc,
		snd:                  sender.New(st, capture.New(st)),
		mux:                  http.NewServeMux(),
		clients:              map[chan string]struct{}{},
		streamStop:           make(chan struct{}),
		iosTCPReachable:      ios.TCPReachable,
		setSetting:           st.SetSetting,
		setSettings:          st.SetSettings,
		sysProxyEnable:       sysproxy.Enable,
		sysProxyDisable:      sysproxy.Disable,
		sysProxyStatus:       sysproxy.Status,
		sysProxySupported:    sysproxy.Supported,
		launchTestBrowser:    testbrowser.Launch,
		testBrowserAvailable: testbrowser.Available,
		gcBodiesFn:           st.GCBodies,
	}
	h.intr = intruder.New(h.snd)
	h.intr.SetBodyReaderResult(h.bodyBytesResult) // lets Intruder grep response bodies
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
		// Per-item delta notifier: a hold/release becomes a small intercept.held /
		// intercept.released SSE event instead of re-broadcasting the whole queue on
		// every change (the O(N^2) fix). A bulk reset still re-sends the snapshot.
		eng.SetChangeNotifier(h.onInterceptChange)
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

// handleMCP serves Streamable-HTTP MCP with a per-request tool client. Internal
// calls stay on loopback while retaining the authenticated caller's credential.
// ?profile= scopes the tool surface (see mcpProfileForRequest); the transport is
// stateless, so a client that wants a profile must send it on every request.
func (h *Hub) handleMCP(w http.ResponseWriter, r *http.Request) {
	profile, err := mcpProfileForRequest(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	srv := mcp.NewProfile(h.loopbackControlBase(), profile)
	srv.SetAuthorization(r.Header.Get("Authorization"))
	srv.SetActivityReporter(h.recordMCPActivity)
	srv.ServeHTTP(w, r)
}

func (h *Hub) recordMCPActivity(a mcp.Activity) {
	if err := h.recordMCPActivityChecked(a); err != nil {
		log.Printf("control: persist MCP activity: %v", err)
	}
}

func (h *Hub) recordMCPActivityChecked(a mcp.Activity) error {
	it, err := (&metaAPI{h}).recordActivity(store.Activity{
		Tool: a.Tool, Summary: a.Summary, OK: a.OK, Result: a.Result, Ms: a.Ms, Intent: a.Intent,
	})
	if err != nil {
		return err
	}
	h.broadcast(map[string]any{"type": "activity", "item": it})
	return nil
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
	ID                  int64               `json:"id"`
	TS                  int64               `json:"ts"`
	Method              string              `json:"method"`
	Scheme              string              `json:"scheme"`
	Host                string              `json:"host"`
	Port                int                 `json:"port"`
	Path                string              `json:"path"`
	Status              int                 `json:"status"`
	Mime                string              `json:"mime"`
	ReqLen              int64               `json:"reqLen"`
	ResLen              int64               `json:"resLen"`
	DurationMs          int64               `json:"durationMs"`
	ClientAddr          string              `json:"clientAddr"`
	Error               string              `json:"error"`
	Flags               int64               `json:"flags"`
	Note                string              `json:"note"`
	OriginalReqBodyHash string              `json:"originalReqBodyHash,omitempty"`
	OriginalResBodyHash string              `json:"originalResBodyHash,omitempty"`
	OriginalReqHeaders  map[string][]string `json:"originalReqHeaders,omitempty"`
	OriginalResHeaders  map[string][]string `json:"originalResHeaders,omitempty"`
	Tags                []string            `json:"tags,omitempty"`
}

type flowDetailJSON struct {
	flowJSON
	HTTPVersion string              `json:"httpVersion"`
	ReqHeaders  map[string][]string `json:"reqHeaders"`
	ResHeaders  map[string][]string `json:"resHeaders"`
	ReqBodyHash string              `json:"reqBodyHash"`
	ResBodyHash string              `json:"resBodyHash"`
	// InterceptionProvenance states how this flow's traffic was obtained: the
	// interception setup version in force at capture time. Omitted when no
	// setup was recorded or it did not cover the flow's host.
	InterceptionProvenance *store.InterceptionProvenance `json:"interceptionProvenance,omitempty"`
}

type ruleJSON struct {
	ID      int64  `json:"id"`
	Ord     int    `json:"ord"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	Match   string `json:"match"`
	Replace string `json:"replace"`
	BigBody bool   `json:"bigBody,omitempty"`
}

type heldJSON struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	Raw    string `json:"raw,omitempty"`
	Len    int    `json:"len,omitempty"`
	// Binary: Raw carries the head only; the body stays on the server (see
	// intercept_binary.go). BodyLen is that body's size.
	Binary  bool `json:"binary,omitempty"`
	BodyLen int  `json:"bodyLen,omitempty"`
	// DropPending: an operator scheduled a drop (Undo window); it cannot be
	// forwarded and turning intercept off drops it.
	DropPending bool `json:"dropPending,omitempty"`
}

type interceptJSON struct {
	// Seq is the intercept engine's generation for this snapshot. The UI stores
	// it and applies a later intercept.held/intercept.released delta only when the
	// delta's seq is exactly one greater; a gap triggers a fresh snapshot. It makes
	// the lossy SSE delta stream self-correcting.
	Seq             int64      `json:"seq"`
	Enabled         bool       `json:"enabled"`
	Queue           []heldJSON `json:"queue"`
	ResponseEnabled bool       `json:"responseEnabled"`
	ResponseQueue   []heldJSON `json:"responseQueue"`
	FilterEnabled   bool       `json:"filterEnabled"`
	FilterTarget    string     `json:"filterTarget"`
	FilterPattern   string     `json:"filterPattern"`
}

type settingsJSON struct {
	ProxyAddr                  string   `json:"proxyAddr"`
	ProxyAddrs                 []string `json:"proxyAddrs,omitempty"`
	ControlAddr                string   `json:"controlAddr"`
	InterceptEnabled           bool     `json:"interceptEnabled"`
	UpstreamProxy              string   `json:"upstreamProxy"` // password removed; see UpstreamProxyPasswordSet
	UpstreamProxyPasswordSet   bool     `json:"upstreamProxyPasswordSet"`
	UpstreamProxyCA            string   `json:"upstreamProxyCA"`
	OobEnabled                 bool     `json:"oobEnabled"`
	CaptureScopeOnly           bool     `json:"captureScopeOnly"`
	SuppressBrowserTelemetry   bool     `json:"suppressBrowserTelemetry"`
	SuppressAndroidTelemetry   bool     `json:"suppressAndroidTelemetry"`
	InvisibleProxy             bool     `json:"invisibleProxy"`
	FindingsUIEditing          bool     `json:"findingsUIEditing"`
	OriginTLSVerify            bool     `json:"originTLSVerify"`
	TLSBypassHosts             []string `json:"tlsBypassHosts"`
	OriginTLSVerifyBypassHosts []string `json:"originTLSVerifyBypassHosts"`
	AutoBypassOnPinFailure     bool     `json:"autoBypassOnPinFailure"`
	ProxyAuthEnabled           bool     `json:"proxyAuthEnabled"`
	ProxyAuthUser              string   `json:"proxyAuthUser"`
	ProxyAuthPassword          string   `json:"proxyAuthPassword"` // always "": write-only
	ProxyAuthPasswordSet       bool     `json:"proxyAuthPasswordSet"`
	// ProxyAuthPasswordMissing: auth is on but no password is saved (a restored
	// backup never carries it), so the listener answers every client with 407.
	ProxyAuthPasswordMissing bool `json:"proxyAuthPasswordMissing"`
	// UpstreamProxyPasswordMissing: the upstream proxy came from a backup that
	// dropped its saved password; it must be re-entered.
	UpstreamProxyPasswordMissing bool   `json:"upstreamProxyPasswordMissing"`
	DeviceProxy                  string `json:"deviceProxy,omitempty"`
	DeviceProxyMode              string `json:"deviceProxyMode,omitempty"`
}

// tlsBypassSettingKey stores the newline-separated host patterns that bypass MITM.
const tlsBypassSettingKey = "proxy.tlsBypassHosts"

const originTLSVerifySettingKey = "proxy.originTLSVerify"

const originTLSVerifyBypassSettingKey = "proxy.originTLSVerifyBypassHosts"

const (
	proxyAuthEnabledKey  = "proxy.authEnabled"
	proxyAuthUserKey     = "proxy.authUser"
	proxyAuthPasswordKey = "proxy.authPassword"

	upstreamPasswordRemovedKey = store.UpstreamPasswordRemovedKey
)

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
		OriginalReqBodyHash: f.OriginalReqBodyHash, OriginalResBodyHash: f.OriginalResBodyHash,
		OriginalReqHeaders: f.OriginalReqHeaders, OriginalResHeaders: f.OriginalResHeaders,
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
	if !decodeLimitedJSON(w, r, maxFlowMetadataRequestBytes, &in) {
		return
	}
	if err := h.st.SetFlowNote(id, in.Note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpErr(w, http.StatusNotFound, "flow not found")
			return
		}
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

const maxFlowMetadataRequestBytes int64 = 64 << 10

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
	if !decodeLimitedJSON(w, r, maxFlowMetadataRequestBytes, &in) {
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
	if !decodeLimitedJSON(w, r, maxBulkRequestBytes, &in) {
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
	if err := h.st.MutateFlowTags(in.FlowIDs, in.Add, in.Remove); err != nil {
		httpInternalErr(w, err)
		return
	}
	for _, id := range in.FlowIDs {
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
	if !decodeLimitedJSON(w, r, maxFlowMetadataRequestBytes, &in) {
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
const maxBulkRequestBytes int64 = 8 << 20

func (h *flowAPI) deleteFlows(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeLimitedJSON(w, r, maxBulkRequestBytes, &in) {
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
// (Intruder) is excluded as noise.
func (h *flowAPI) listEndpoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EndpointFilter{
		Host:          q.Get("host"),
		Search:        q.Get("search"),
		SearchScope:   q.Get("searchScope"),
		ExcludeFlags:  store.FlagIntruder,
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
	// History hides Repeater/Intruder by default (they have their own
	// views). includeTools=1 is the escape hatch for MCP agents / triage that
	// need to see tool-generated traffic alongside proxy captures.
	if q.Get("includeTools") != "1" {
		f.ExcludeFlags = store.FlagRepeater | store.FlagIntruder
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
	case showManual && showAI:
		f.IncludeFlags = store.FlagAI
	case showAI && !showManual:
		f.RequireFlags |= store.FlagAI
		f.IncludeFlags = store.FlagAI
	case showManual && !showAI:
		f.WithoutFlags = store.FlagAI
	}
	// Collection-sent flows are History traffic by default (FlagCollection, a
	// COLL chip in the UI). collection=0 hides them, collection=only shows
	// nothing else; both compose with the source filters above.
	switch q.Get("collection") {
	case "0":
		f.WithoutFlags |= store.FlagCollection
	case "only":
		f.RequireFlags |= store.FlagCollection
	}
	if sc := q.Get("status"); sc != "" {
		f.StatusClass = atoiOr(sc, 0)
	}
	f.SearchScope = normalizeFlowSearchScope(q.Get("searchScope"))
	w.Header().Set("X-Interseptor-Search-Scope", f.SearchScope)
	if q.Get("hasNote") == "1" {
		f.HasNote = true
	}
	f.HideBrowserNoise = q.Get("hideBrowserNoise") == "1"
	f.Tag = q.Get("tag")
	// Negative filters (repeatable): notMethod, notHost, notPath, notStatus.
	f.NotMethods, f.NotHosts, f.NotPaths = q["notMethod"], q["notHost"], q["notPath"]
	for _, s := range q["notStatus"] {
		if n := atoiOr(s, 0); n > 0 {
			f.NotStatuses = append(f.NotStatuses, n)
		}
	}
	searchScope := f.SearchScope
	searchNote, emptySearch, err := h.applyFlowSearch(r, &f)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if emptySearch {
		writeJSON(w, http.StatusOK, map[string]any{"flows": []flowJSON{}, "truncated": false, "searchNote": searchNote})
		return
	}
	if searchScope == "body" && strings.TrimSpace(f.Search) != "" {
		ids, note, bound, err := h.st.FlowIDsBodySearch(f, 8000)
		if err != nil {
			httpInternalErr(w, err)
			return
		}
		searchNote = note
		f.Search = ""
		f.FlowIDs = ids
		f.ScanBoundID = bound
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, withSearchMore(map[string]any{"flows": []flowJSON{}, "truncated": false, "searchNote": note}, false, bound))
			return
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
		resp := withSearchMore(map[string]any{"flows": out, "truncated": truncated}, truncated, f.ScanBoundID)
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
	resp := withSearchMore(map[string]any{"flows": out, "truncated": truncated}, truncated, f.ScanBoundID)
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
		httpNotFoundOrInternal(w, err, "flow not found")
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
	prov, _ := h.st.InterceptionProvenance(f.TS, f.Host) // best-effort metadata
	writeJSON(w, http.StatusOK, flowDetailJSON{
		InterceptionProvenance: prov,
		flowJSON:               toFlowJSON(f),
		HTTPVersion:            f.HTTPVersion,
		ReqHeaders:             f.ReqHeaders,
		ResHeaders:             f.ResHeaders,
		ReqBodyHash:            f.ReqBodyHash,
		ResBodyHash:            f.ResBodyHash,
	})
}

func (h *flowAPI) getFlowRaw(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	original := r.URL.Query().Get("variant") == "original"
	var raw []byte
	var err error
	if r.URL.Query().Get("side") == "res" {
		raw, err = h.rawResponseVariantResult(f, original)
	} else {
		raw, err = h.rawRequestVariantResult(f, original)
	}
	if err != nil {
		httpFileNotFoundOrInternal(w, err, "flow body not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(raw)
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
	var headers map[string][]string
	var hash string
	if side == "res" {
		mimeType = f.Mime
		headers = f.ResHeaders
		hash = f.ResBodyHash
	} else {
		headers = f.ReqHeaders
		hash = f.ReqBodyHash
		mimeType = headerContentType(f.ReqHeaders)
	}
	enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding")))
	var stream io.ReadCloser
	var body []byte
	var err error
	if !supportsDisplayEncoding(enc) {
		stream, err = h.st.OpenBody(hash)
	} else {
		var overflow bool
		body, overflow, err = h.bodyBytesUpTo(hash, decodeMax)
		if err == nil && overflow {
			stream, err = h.st.OpenBody(hash)
		} else if err == nil {
			_, body = decodeForDisplay(headers, body)
		}
	}
	if err != nil {
		httpFileNotFoundOrInternal(w, err, "body not found")
		return
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
	if stream != nil {
		defer stream.Close()
		if _, err := io.Copy(w, stream); err != nil {
			log.Printf("control: stream flow body: %v", err)
		}
		return
	}
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
	return h.rawRequestVariant(f, false)
}

func (h *Hub) rawRequestVariant(f *store.Flow, original bool) []byte {
	raw, _ := h.rawRequestVariantResult(f, original)
	return raw
}

func (h *Hub) rawRequestVariantResult(f *store.Flow, original bool) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s %s\r\n", f.Method, orVal(f.Path, "/"), orVal(f.HTTPVersion, "HTTP/1.1"))
	hdrs, hash := f.ReqHeaders, f.ReqBodyHash
	if original {
		if f.OriginalReqHeaders != nil {
			hdrs = f.OriginalReqHeaders
		}
		if f.OriginalReqBodyHash != "" {
			hash = f.OriginalReqBodyHash
		}
	}
	body, err := h.bodyBytesResult(hash)
	if err != nil {
		return nil, err
	}
	hdr, body := decodeForDisplay(hdrs, body)
	writeHeaders(&b, hdr, f.Host)
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes(), nil
}

func (h *Hub) rawResponseVariant(f *store.Flow, original bool) []byte {
	raw, _ := h.rawResponseVariantResult(f, original)
	return raw
}

func (h *Hub) rawResponseVariantResult(f *store.Flow, original bool) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %d %s\r\n", orVal(f.HTTPVersion, "HTTP/1.1"), f.Status, http.StatusText(f.Status))
	hdrs, hash := f.ResHeaders, f.ResBodyHash
	if original {
		if f.OriginalResHeaders != nil {
			hdrs = f.OriginalResHeaders
		}
		if f.OriginalResBodyHash != "" {
			hash = f.OriginalResBodyHash
		}
	}
	body, err := h.bodyBytesResult(hash)
	if err != nil {
		return nil, err
	}
	hdr, body := decodeForDisplay(hdrs, body)
	writeHeaders(&b, hdr, "")
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes(), nil
}
func (h *Hub) rawResponse(f *store.Flow) []byte {
	return h.rawResponseVariant(f, false)
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
	b, _ := h.bodyBytesResult(hash)
	return b
}

func (h *Hub) bodyBytesResult(hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (h *Hub) bodyBytesUpTo(hash string, limit int64) ([]byte, bool, error) {
	if hash == "" {
		return nil, false, nil
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > limit {
		return nil, true, nil
	}
	return b, false, nil
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
	if !decodeLimitedJSON(w, r, maxRuleRequestBytes, &in) {
		return
	}
	if !validRule(w, in) {
		return
	}
	rule := store.Rule{Ord: in.Ord, Enabled: in.Enabled, Type: in.Type, Match: in.Match, Replace: in.Replace, BigBody: in.BigBody}
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
	if !decodeLimitedJSON(w, r, maxRuleRequestBytes, &in) {
		return
	}
	in.ID = id
	if !validRule(w, in) {
		return
	}
	if err := h.st.UpdateRule(&store.Rule{ID: id, Ord: in.Ord, Enabled: in.Enabled, Type: in.Type, Match: in.Match, Replace: in.Replace, BigBody: in.BigBody}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpErr(w, http.StatusNotFound, "rule not found")
			return
		}
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
const (
	maxRulePattern          = 4096
	maxRuleRequestBytes     = 16 << 10
	maxInterceptConfigBytes = 16 << 10
)

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
	// Compile-check the regex the way the engine does (regexp.Compile). The
	// engine's own error names a rule id, which is still 0 here.
	if _, err := regexp.Compile(in.Match); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid match regex: "+err.Error())
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
	hj := heldJSON{ID: held.ID, Len: len(held.Raw), DropPending: held.DropPending}
	if held.Flow != nil {
		hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
	}
	if includeRaw {
		v := heldViewOf(held.Raw)
		hj.Raw, hj.Binary, hj.BodyLen = v.Raw, v.Binary, v.BodyLen
	}
	return hj
}

func heldFromResponse(held intercept.HeldResponse, includeRaw bool) heldJSON {
	hj := heldJSON{ID: held.ID, Len: len(held.Raw), DropPending: held.DropPending}
	if held.Flow != nil {
		hj.Method, hj.Scheme, hj.Host, hj.Path = held.Flow.Method, held.Flow.Scheme, held.Flow.Host, held.Flow.Path
	}
	if includeRaw {
		v := heldViewOf(held.Raw)
		hj.Raw, hj.Binary, hj.BodyLen = v.Raw, v.Binary, v.BodyLen
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
	// One atomic snapshot so the queue contents and Seq cannot drift apart under a
	// concurrent hold/forward (what a delta client reconciles against).
	s := h.eng.Snapshot()
	out.Seq = s.Seq
	out.Enabled = s.Enabled
	for _, held := range s.Queue {
		out.Queue = append(out.Queue, heldFromRequest(held, includeRaw))
	}
	out.ResponseEnabled = s.ResponseEnabled
	for _, held := range s.ResponseQueue {
		out.ResponseQueue = append(out.ResponseQueue, heldFromResponse(held, includeRaw))
	}
	out.FilterEnabled, out.FilterTarget, out.FilterPattern = s.FilterEnabled, s.FilterTarget, s.FilterPattern
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
	raw, ok := h.heldRawByID(id, r.URL.Query().Get("side") == "resp")
	if !ok {
		httpErr(w, http.StatusNotFound, "not held")
		return
	}
	writeJSON(w, http.StatusOK, heldViewOf(raw))
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
	if !decodeLimitedJSON(w, r, maxInterceptConfigBytes, &in) {
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
	if !decodeLimitedJSON(w, r, maxInterceptConfigBytes, &in) {
		return
	}
	if len(in.Pattern) > maxRulePattern {
		httpErr(w, http.StatusBadRequest, "pattern too long")
		return
	}
	target := in.Target
	if target == "" {
		target = "any"
	}
	enabled := in.Enabled && in.Pattern != ""
	if enabled {
		if _, err := regexp.Compile(in.Pattern); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid regex: "+err.Error())
			return
		}
	}
	if err := h.st.SetSettings(map[string]string{
		"intercept.filter.enabled": boolToFlag(enabled),
		"intercept.filter.target":  target,
		"intercept.filter.pattern": in.Pattern,
	}); err != nil {
		httpInternalErr(w, err)
		return
	}
	if err := h.eng.SetInterceptFilter(enabled, target, in.Pattern); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid regex: "+err.Error())
		return
	}
	// The UI saves the filter as the operator types: answer with the summary
	// (no held raw bytes), as the SSE stream does; raw loads on demand.
	writeJSON(w, http.StatusOK, h.interceptStateSummary())
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
		Raw      string `json:"raw"`
		KeepBody bool   `json:"keepBody"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	if err := h.eng.Forward(id, h.forwardRaw(id, false, in.Raw, in.KeepBody)); err != nil {
		httpErr(w, forwardErrStatus(err), err.Error())
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

// forwardErrStatus maps a Forward failure to its HTTP status: a drop-pending
// item is a 409 (undo the drop first), anything else stays a 400.
func forwardErrStatus(err error) int {
	if errors.Is(err, intercept.ErrDropPending) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// setDropPending marks or clears a scheduled drop on a held request
// Body: {pending}. The UI marks an item when
// its Drop Undo window opens and clears it on Undo, so a bulk release from any
// client drops the item instead of sending it.
func (h *interceptAPI) setDropPending(w http.ResponseWriter, r *http.Request) {
	h.markDropPending(w, r, h.eng.SetDropPending)
}

func (h *interceptAPI) setResponseDropPending(w http.ResponseWriter, r *http.Request) {
	h.markDropPending(w, r, h.eng.SetResponseDropPending)
}

func (h *interceptAPI) markDropPending(w http.ResponseWriter, r *http.Request, mark func(int64, bool) error) {
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
		Pending bool `json:"pending"`
	}
	if !decodeLimitedJSON(w, r, maxInterceptConfigBytes, &in) {
		return
	}
	if err := mark(id, in.Pending); err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
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
	if !decodeLimitedJSON(w, r, maxInterceptConfigBytes, &in) {
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
		Raw      string `json:"raw"`
		KeepBody bool   `json:"keepBody"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	if err := h.eng.ForwardResponse(id, h.forwardRaw(id, true, in.Raw, in.KeepBody)); err != nil {
		httpErr(w, forwardErrStatus(err), err.Error())
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

// ResolveCaptureScopeOnly decides whether out-of-scope response/request bodies
// are dropped from capture. An explicit stored choice always wins. With no
// stored choice the default is to drop them exactly when the operator has set an
// allow-list (active include rules): defining a target scope signals they do not
// want full out-of-scope bodies written to disk by surprise (issue #108). With
// no include rules the default stays off, preserving capture-everything. Both
// cmd startup and GET /api/settings use this so the behaviour, the proxy, and the
// UI toggle agree.
func ResolveCaptureScopeOnly(stored string, ok bool, hasIncludes bool) bool {
	if ok {
		return stored == "1"
	}
	return hasIncludes
}

func (h *settingsAPI) getSettings(w http.ResponseWriter, r *http.Request) {
	up, _, _ := h.st.GetSetting("upstream.proxy")
	upCA, _, _ := h.st.GetSetting("upstream.proxyCA")
	scopeOnly, scopeOnlySet, _ := h.st.GetSetting("capture.scopeOnly")
	suppressTelemetry, stOK, _ := h.st.GetSetting("capture.suppressBrowserTelemetry")
	suppressTelemetryOn := !stOK || suppressTelemetry == "1"
	suppressAndroid, andOK, _ := h.st.GetSetting("capture.suppressAndroidTelemetry")
	suppressAndroidOn := !andOK || suppressAndroid == "1"
	invisibleProxy, _, _ := h.st.GetSetting("proxy.invisibleProxy")
	findingsUIEditing, _, _ := h.st.GetSetting("findings.uiEditing")
	originTLSVerify, _, _ := h.st.GetSetting(originTLSVerifySettingKey)
	tlsBypassRaw, _, _ := h.st.GetSetting(tlsBypassSettingKey)
	originTLSVerifyBypassRaw, _, _ := h.st.GetSetting(originTLSVerifyBypassSettingKey)
	autoBypass, _, _ := h.st.GetSetting("proxy.autoBypassOnPinFailure")
	proxyAuthEnabled, _, _ := h.st.GetSetting(proxyAuthEnabledKey)
	proxyAuthUser, _, _ := h.st.GetSetting(proxyAuthUserKey)
	proxyAuthPassword, _, _ := h.st.GetSetting(proxyAuthPasswordKey)
	upPasswordRemoved, _, _ := h.st.GetSetting(upstreamPasswordRemovedKey)
	up, upPasswordSet := redactUpstreamPassword(up)
	proxyAddrs := h.currentProxyAddrs()
	deviceEP := h.resolveDeviceEndpoint()
	writeJSON(w, http.StatusOK, settingsJSON{
		ProxyAddr: displayProxyAddrs(proxyAddrs), ProxyAddrs: proxyAddrs,
		DeviceProxy: deviceEP.Endpoint, DeviceProxyMode: loadDeviceProxyMode(h.st),
		ControlAddr: h.currentControlAddr(), InterceptEnabled: h.eng != nil && h.eng.Enabled(),
		UpstreamProxy: up, UpstreamProxyPasswordSet: upPasswordSet, UpstreamProxyCA: upCA, OobEnabled: h.oobEnabled(),
		CaptureScopeOnly: ResolveCaptureScopeOnly(scopeOnly, scopeOnlySet, h.sc.HasIncludes()), SuppressBrowserTelemetry: suppressTelemetryOn,
		SuppressAndroidTelemetry: suppressAndroidOn, InvisibleProxy: invisibleProxy == "1",
		FindingsUIEditing: findingsUIEditing == "1",
		OriginTLSVerify:   originTLSVerify == "1", TLSBypassHosts: parseHostList(tlsBypassRaw),
		OriginTLSVerifyBypassHosts: parseHostList(originTLSVerifyBypassRaw), AutoBypassOnPinFailure: autoBypass == "1",
		ProxyAuthEnabled: proxyAuthEnabled == "1", ProxyAuthUser: proxyAuthUser, ProxyAuthPasswordSet: proxyAuthPassword != "",
		ProxyAuthPasswordMissing:     proxyAuthEnabled == "1" && proxyAuthPassword == "",
		UpstreamProxyPasswordMissing: upPasswordRemoved == "1" && up != "" && !upPasswordSet,
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

// ConfigureSenderUpstreamProxy applies a saved route to Repeater, Intruder,
// and scanner traffic during startup. Runtime Settings updates call the same
// Sender methods inside settingsAPI.
func (h *Hub) ConfigureSenderUpstreamProxy(value string) error {
	return h.snd.SetUpstreamProxy(value)
}

// ConfigureSenderUpstreamProxyCA applies saved HTTPS-proxy trust to Sender
// consumers during startup.
func (h *Hub) ConfigureSenderUpstreamProxyCA(value []byte) error {
	return h.snd.SetUpstreamProxyCA(value)
}

func validateProxyBasicAuth(enabled bool, user, password string) error {
	if strings.ContainsAny(user, "\r\n\x00:") || strings.ContainsAny(password, "\r\n\x00") {
		return errors.New("proxy authentication rejects newlines and a colon in the username")
	}
	if len(user) > 128 || len(password) > 256 {
		return errors.New("proxy authentication username or password is too long")
	}
	if enabled && (user == "" || password == "") {
		return errors.New("proxy authentication requires a username and password")
	}
	return nil
}

// settingsPut is the PUT /api/settings body. Pointer fields distinguish "absent"
// from the zero value so a partial body changes only what it names.
type settingsPut struct {
	ProxyAddr     string   `json:"proxyAddr"`
	ProxyAddrs    []string `json:"proxyAddrs"`
	ControlAddr   string   `json:"controlAddr"`
	UpstreamProxy *string  `json:"upstreamProxy"` // pointer so "" can clear it
	// UpstreamProxyKeepPassword re-uses the saved password when upstreamProxy
	// names a user without a password on the same proxy host (the masked URL
	// GET returned, possibly with the username edited).
	UpstreamProxyKeepPassword  bool      `json:"upstreamProxyKeepPassword"`
	UpstreamProxyCA            *string   `json:"upstreamProxyCA"`
	OobEnabled                 *bool     `json:"oobEnabled"`
	CaptureScopeOnly           *bool     `json:"captureScopeOnly"`
	SuppressBrowserTelemetry   *bool     `json:"suppressBrowserTelemetry"`
	SuppressAndroidTelemetry   *bool     `json:"suppressAndroidTelemetry"`
	InvisibleProxy             *bool     `json:"invisibleProxy"`
	FindingsUIEditing          *bool     `json:"findingsUIEditing"`
	OriginTLSVerify            *bool     `json:"originTLSVerify"`
	TLSBypassHosts             *[]string `json:"tlsBypassHosts"`
	OriginTLSVerifyBypassHosts *[]string `json:"originTLSVerifyBypassHosts"`
	AutoBypassOnPinFailure     *bool     `json:"autoBypassOnPinFailure"`
	ProxyAuthEnabled           *bool     `json:"proxyAuthEnabled"`
	ProxyAuthUser              *string   `json:"proxyAuthUser"`
	ProxyAuthPassword          *string   `json:"proxyAuthPassword"`
}

// settingsUpdate is a fully validated settings change. It is committed in three
// phases so a rejected or failed request leaves nothing half-applied:
//  1. runtime steps that can fail (listener rebinds, upstream route and trust),
//     each paired with an undo;
//  2. one atomic store.SetSettings batch for every key;
//  3. infallible in-memory setters, then a single settings.update broadcast.
type settingsUpdate struct {
	values  map[string]string
	runtime []settingsRuntimeStep
	apply   []func()
}

type settingsRuntimeStep struct {
	do   func() error // a returned error rejects the request with 400
	undo func()
}

// settingsUpdateError carries the HTTP status for a rejected settings update.
type settingsUpdateError struct {
	status int
	err    error
}

func (e *settingsUpdateError) Error() string { return e.err.Error() }

func settingsBadRequest(err error) error {
	return &settingsUpdateError{status: http.StatusBadRequest, err: err}
}

func settingFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func (u *settingsUpdate) toggle(p *bool, key string, set func(bool)) {
	if p == nil {
		return
	}
	on := *p
	u.values[key] = settingFlag(on)
	if set != nil {
		u.apply = append(u.apply, func() { set(on) })
	}
}

func (u *settingsUpdate) hostList(p *[]string, key string, set func([]string)) {
	if p == nil {
		return
	}
	hosts := parseHostList(strings.Join(*p, "\n"))
	u.values[key] = strings.Join(hosts, "\n")
	if set != nil {
		u.apply = append(u.apply, func() { set(hosts) })
	}
}

func (h *settingsAPI) planSettingsUpdate(in *settingsPut) (*settingsUpdate, error) {
	u := &settingsUpdate{values: map[string]string{}}
	u.toggle(in.OobEnabled, "oob.enabled", nil)
	u.toggle(in.CaptureScopeOnly, "capture.scopeOnly", h.SetCaptureScopeOnly)
	u.toggle(in.SuppressBrowserTelemetry, "capture.suppressBrowserTelemetry", h.SetSuppressBrowserTelemetry)
	u.toggle(in.SuppressAndroidTelemetry, "capture.suppressAndroidTelemetry", h.SetSuppressAndroidTelemetry)
	u.toggle(in.InvisibleProxy, "proxy.invisibleProxy", h.SetInvisibleProxy)
	u.toggle(in.FindingsUIEditing, "findings.uiEditing", nil)
	u.hostList(in.TLSBypassHosts, tlsBypassSettingKey, h.SetTLSBypassHosts)
	u.hostList(in.OriginTLSVerifyBypassHosts, originTLSVerifyBypassSettingKey, h.SetOriginTLSVerifyBypassHosts)
	u.toggle(in.AutoBypassOnPinFailure, "proxy.autoBypassOnPinFailure", h.SetAutoBypassOnPinFailure)
	u.toggle(in.OriginTLSVerify, originTLSVerifySettingKey, h.SetOriginTLSVerify)
	if err := h.planProxyBasicAuth(u, in.ProxyAuthEnabled, in.ProxyAuthUser, in.ProxyAuthPassword); err != nil {
		return nil, err
	}
	if err := h.planUpstreamProxyCA(u, in.UpstreamProxyCA); err != nil {
		return nil, err
	}
	if err := h.planUpstreamProxy(u, in.UpstreamProxy, in.UpstreamProxyKeepPassword); err != nil {
		return nil, err
	}
	if err := h.planProxyAddrs(u, in.ProxyAddrs, in.ProxyAddr); err != nil {
		return nil, err
	}
	if err := h.planControlAddr(u, in.ControlAddr); err != nil {
		return nil, err
	}
	return u, nil
}

func (h *settingsAPI) planProxyBasicAuth(u *settingsUpdate, enabled *bool, user, password *string) error {
	if enabled == nil && user == nil && password == nil {
		return nil
	}
	currentEnabled, _, err := h.st.GetSetting(proxyAuthEnabledKey)
	if err != nil {
		return err
	}
	currentUser, _, err := h.st.GetSetting(proxyAuthUserKey)
	if err != nil {
		return err
	}
	currentPassword, _, err := h.st.GetSetting(proxyAuthPasswordKey)
	if err != nil {
		return err
	}
	on := currentEnabled == "1"
	if enabled != nil {
		on = *enabled
	}
	if user != nil {
		currentUser = strings.TrimSpace(*user)
	}
	if password != nil {
		currentPassword = *password
	}
	if err := validateProxyBasicAuth(on, currentUser, currentPassword); err != nil {
		return settingsBadRequest(err)
	}
	u.values[proxyAuthEnabledKey] = settingFlag(on)
	u.values[proxyAuthUserKey] = currentUser
	u.values[proxyAuthPasswordKey] = currentPassword
	if h.SetProxyBasicAuth != nil {
		u.apply = append(u.apply, func() { h.SetProxyBasicAuth(on, currentUser, currentPassword) })
	}
	return nil
}

func (h *settingsAPI) planUpstreamProxyCA(u *settingsUpdate, raw *string) error {
	if raw == nil {
		return nil
	}
	value := strings.TrimSpace(*raw)
	previous, _, err := h.st.GetSetting("upstream.proxyCA")
	if err != nil {
		return err
	}
	u.values["upstream.proxyCA"] = value
	u.runtime = append(u.runtime, settingsRuntimeStep{
		do: func() error {
			if err := h.snd.SetUpstreamProxyCA([]byte(value)); err != nil {
				return err
			}
			if h.SetUpstreamProxyCA != nil {
				if err := h.SetUpstreamProxyCA([]byte(value)); err != nil {
					_ = h.snd.SetUpstreamProxyCA([]byte(previous))
					return err
				}
			}
			return nil
		},
		undo: func() {
			if h.SetUpstreamProxyCA != nil {
				_ = h.SetUpstreamProxyCA([]byte(previous))
			}
			_ = h.snd.SetUpstreamProxyCA([]byte(previous))
		},
	})
	return nil
}

func (h *settingsAPI) planUpstreamProxy(u *settingsUpdate, raw *string, keepPassword bool) error {
	if raw == nil {
		return nil
	}
	value := *raw
	previous, _, err := h.st.GetSetting("upstream.proxy")
	if err != nil {
		return err
	}
	if keepPassword {
		value = keepUpstreamPassword(value, previous)
	}
	u.values["upstream.proxy"] = value
	u.values[upstreamPasswordRemovedKey] = "" // an explicit save settles the backup gap
	u.runtime = append(u.runtime, settingsRuntimeStep{
		do: func() error {
			if err := h.snd.SetUpstreamProxy(value); err != nil {
				return err
			}
			if h.Upstream != nil {
				if err := h.Upstream(value); err != nil {
					_ = h.snd.SetUpstreamProxy(previous)
					return err
				}
			}
			return nil
		},
		undo: func() {
			_ = h.snd.SetUpstreamProxy(previous)
			if h.Upstream != nil {
				_ = h.Upstream(previous)
			}
		},
	})
	return nil
}

func (h *settingsAPI) rebindProxyAddrs(addrs []string) error {
	if mr, ok := h.rebind.(MultiProxyRebinder); ok {
		return mr.RebindAddrs(addrs)
	}
	if h.rebind != nil && len(addrs) == 1 {
		return h.rebind.Rebind(addrs[0])
	}
	return nil
}

func (h *settingsAPI) planProxyAddrs(u *settingsUpdate, addrs []string, addr string) error {
	if len(addrs) == 0 && addr != "" {
		addrs = []string{addr}
	}
	previous := h.currentProxyAddrs()
	if len(addrs) == 0 || proxyAddrsEqual(addrs, previous) {
		return nil
	}
	addrs = normalizeProxyAddrs(addrs)
	if err := validateProxyAddrs(addrs); err != nil {
		return settingsBadRequest(err)
	}
	if _, multi := h.rebind.(MultiProxyRebinder); !multi && len(addrs) > 1 {
		return settingsBadRequest(errors.New("multiple proxy listeners are not supported by this build"))
	}
	u.values["proxy.addrs"] = formatProxyAddrs(addrs)
	u.values["proxy.addr"] = addrs[0]
	u.runtime = append(u.runtime, settingsRuntimeStep{
		do: func() error {
			if err := h.rebindProxyAddrs(addrs); err != nil {
				return fmt.Errorf("rebind failed: %w", err)
			}
			return nil
		},
		undo: func() { _ = h.rebindProxyAddrs(previous) },
	})
	if h.SyncSelfPorts != nil {
		u.apply = append(u.apply, h.SyncSelfPorts)
	}
	return nil
}

func (h *settingsAPI) planControlAddr(u *settingsUpdate, addr string) error {
	previous := h.currentControlAddr()
	if addr == "" || addr == previous {
		return nil
	}
	if err := validateListenAddr(addr); err != nil {
		return settingsBadRequest(err)
	}
	if !isLoopbackHost(addr) && !bind.ExternalBindAllowed() {
		return settingsBadRequest(errors.New("control bind address must be loopback (127.0.0.1/localhost/::1); external bind is disabled (INTERSEPTOR_ALLOW_EXTERNAL_BIND=0)"))
	}
	u.values["control.addr"] = addr
	if h.ctrlRebind != nil {
		u.runtime = append(u.runtime, settingsRuntimeStep{
			do: func() error {
				if err := h.ctrlRebind.Rebind(addr); err != nil {
					return fmt.Errorf("control rebind failed: %w", err)
				}
				return nil
			},
			undo: func() { _ = h.ctrlRebind.Rebind(previous) },
		})
	}
	u.apply = append(u.apply, func() { h.SetSelfAddr(addr) })
	if h.SyncSelfPorts != nil {
		u.apply = append(u.apply, h.SyncSelfPorts)
	}
	return nil
}

// commitSettingsUpdate runs the runtime steps, persists every key in one
// transaction, and only then applies the in-memory setters. Any failure undoes
// the runtime steps already taken, so the store and the live proxy never
// diverge and a rejected request changes nothing.
func (h *settingsAPI) commitSettingsUpdate(u *settingsUpdate) error {
	done := 0
	rollback := func() {
		for i := done - 1; i >= 0; i-- {
			u.runtime[i].undo()
		}
	}
	for _, step := range u.runtime {
		if err := step.do(); err != nil {
			rollback()
			return settingsBadRequest(err)
		}
		done++
	}
	if len(u.values) > 0 {
		if err := h.setSettings(u.values); err != nil {
			rollback()
			return err
		}
	}
	for _, apply := range u.apply {
		apply()
	}
	if len(u.values) > 0 {
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	return nil
}

func writeSettingsUpdateErr(w http.ResponseWriter, err error) {
	var bad *settingsUpdateError
	if errors.As(err, &bad) {
		httpErr(w, bad.status, bad.Error())
		return
	}
	httpInternalErr(w, err)
}

func (h *settingsAPI) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsPut
	if !decodeLimitedJSON(w, r, maxRequestBody, &in) {
		return
	}
	u, err := h.planSettingsUpdate(&in)
	if err == nil {
		err = h.commitSettingsUpdate(u)
	}
	if err != nil {
		writeSettingsUpdateErr(w, err)
		return
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
	asset, ok := loadUIAsset(name)
	if !ok {
		if name == "index.html" {
			httpErr(w, http.StatusInternalServerError, "ui missing")
			return
		}
		http.NotFound(w, r)
		return
	}
	// The UI is embedded and changes with every build, and the modules have no
	// version hash: writeUIAsset makes the browser revalidate every asset
	// (no-cache + ETag, 304 when unchanged) and gzips text when accepted.
	writeUIAsset(w, r, name, asset)
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
	case strings.HasSuffix(name, ".webmanifest"):
		return "application/manifest+json"
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

// httpNotFoundOrInternal reserves 404 for an absent SQLite row. Operational
// failures are logged and scrubbed instead of masquerading as missing data.
func httpNotFoundOrInternal(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, sql.ErrNoRows) {
		httpErr(w, http.StatusNotFound, notFound)
		return
	}
	httpInternalErr(w, err)
}

func httpTextNotFoundOrInternal(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, notFound, http.StatusNotFound)
		return
	}
	log.Printf("control: 500: %v", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func httpFileNotFoundOrInternal(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, fs.ErrNotExist) {
		httpErr(w, http.StatusNotFound, notFound)
		return
	}
	httpInternalErr(w, err)
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
