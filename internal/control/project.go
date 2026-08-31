package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

// projectBundle is a portable session: captured flows (as HAR), match-&-replace
// rules, target-scope rules, and selected settings.
type projectBundle struct {
	Version  string            `json:"version"`
	HAR      json.RawMessage   `json:"har"`
	Rules    []store.Rule      `json:"rules"`
	Scope    []store.ScopeRule `json:"scope"`
	Settings map[string]string `json:"settings"`
	Notes    string            `json:"notes,omitempty"`
}

const maxPortableProjectImportBytes = 128 << 20

const maxProjectSwitchRequestBytes int64 = 16 << 10

const portableProjectFlowPageSize = 10000

func (h *projectAPI) exportProject(w http.ResponseWriter, r *http.Request) {
	flows, err := portableProjectFlows(h.st, portableProjectFlowPageSize)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if err := validatePortableProjectBodies(h.st, flows); err != nil {
		httpInternalErr(w, err)
		return
	}
	rules, err := h.st.ListRules()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	scope, err := h.st.ListScopeRules()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	readSetting := func(key string) (string, error) {
		value, _, err := h.st.GetSetting(key)
		return value, err
	}
	up, err := readSetting("upstream.proxy")
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	upCA, err := readSetting("upstream.proxyCA")
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	authz, err := readSetting("authz.identities")
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	originVerify, err := readSetting(originTLSVerifySettingKey)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if originVerify != "1" {
		originVerify = "0"
	}
	originBypass, err := readSetting(originTLSVerifyBypassSettingKey)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	notes, err := h.st.LoadNotes()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	bundle := projectBundle{
		Version: "1", HAR: json.RawMessage(harx.Build(flows, h.bodyBytes)), Rules: rules, Scope: scope, Notes: notes,
		Settings: map[string]string{"upstream.proxy": up, "upstream.proxyCA": upCA, "authz.identities": authz,
			originTLSVerifySettingKey: originVerify, originTLSVerifyBypassSettingKey: originBypass},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="interseptor-project.json"`)
	json.NewEncoder(w).Encode(bundle)
}

func portableProjectFlows(st *store.Store, pageSize int) ([]*store.Flow, error) {
	var flows []*store.Flow
	var beforeID int64
	for {
		page, err := st.QueryFlowsFilter(store.FlowFilter{
			Limit:        pageSize,
			BeforeID:     beforeID,
			ExcludeFlags: store.FlagIntruder,
		})
		if err != nil {
			return nil, err
		}
		flows = append(flows, page...)
		if len(page) < pageSize {
			return flows, nil
		}
		beforeID = page[len(page)-1].ID
	}
}

func validatePortableProjectBodies(st *store.Store, flows []*store.Flow) error {
	seen := make(map[string]struct{})
	for _, flow := range flows {
		for _, hash := range []string{flow.ReqBodyHash, flow.ResBodyHash} {
			if hash == "" {
				continue
			}
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			rc, err := st.OpenBody(hash)
			if err != nil {
				return fmt.Errorf("open flow body %s: %w", hash, err)
			}
			if err := rc.Close(); err != nil {
				return fmt.Errorf("close flow body %s: %w", hash, err)
			}
		}
	}
	return nil
}

// importProject merges a project into the current session (additive for flows,
// rules, and scope; applies the upstream-proxy setting). It does not rebind the
// proxy listener.
func (h *projectAPI) importProject(w http.ResponseWriter, r *http.Request) {
	data, ok := readLimitedBody(w, r, maxPortableProjectImportBytes)
	if !ok {
		return
	}
	var bundle projectBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		httpErr(w, http.StatusBadRequest, "not a valid project: "+err.Error())
		return
	}

	flows := 0
	if harData := bytes.TrimSpace(bundle.HAR); len(harData) > 0 && !bytes.Equal(harData, []byte("null")) {
		entries, err := harx.Parse(harData)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "project contains an invalid HAR: "+err.Error())
			return
		}
		for _, e := range entries {
			u, err := url.Parse(e.URL)
			if err != nil || !u.IsAbs() || u.Host == "" {
				continue
			}
			ts := e.TS
			if ts.IsZero() {
				ts = time.Now()
			}
			fl := &store.Flow{
				TS: ts, Method: e.Method, Scheme: u.Scheme, Host: u.Hostname(),
				Port: atoiOr(u.Port(), defaultPortFor(u.Scheme)), Path: u.RequestURI(),
				HTTPVersion: orVal(e.HTTPVersion, "HTTP/1.1"), Status: e.Status,
				ReqHeaders: e.ReqHeaders, ResHeaders: e.ResHeaders, Mime: e.Mime,
				DurationMs: e.DurationMs, Flags: store.FlagImported,
			}
			if err := h.insertImportedFlow(fl, e.ReqBody, e.ResBody); err != nil {
				httpInternalErr(w, err)
				return
			}
			flows++
		}
	}
	rulesImported := 0
	for i := range bundle.Rules {
		bundle.Rules[i].ID = 0
		if _, err := h.st.CreateRule(&bundle.Rules[i]); err != nil {
			httpInternalErr(w, err)
			return
		}
		rulesImported++
	}
	scopeImported := 0
	for i := range bundle.Scope {
		bundle.Scope[i].ID = 0
		if _, err := h.st.CreateScopeRule(&bundle.Scope[i]); err != nil {
			httpInternalErr(w, err)
			return
		}
		scopeImported++
	}
	upCA, hasUpCA := bundle.Settings["upstream.proxyCA"]
	previousUp, _, _ := h.st.GetSetting("upstream.proxy")
	if hasUpCA {
		upCA = strings.TrimSpace(upCA)
		previousCA, _, err := h.st.GetSetting("upstream.proxyCA")
		if err != nil {
			httpInternalErr(w, err)
			return
		}
		if h.SetUpstreamProxyCA != nil {
			if err := h.SetUpstreamProxyCA([]byte(upCA)); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := h.snd.SetUpstreamProxyCA([]byte(upCA)); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if up, ok := bundle.Settings["upstream.proxy"]; ok && up != "" && h.Upstream != nil {
			if err := h.snd.SetUpstreamProxy(up); err != nil {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := h.Upstream(up); err != nil {
				_ = h.snd.SetUpstreamProxy(previousUp)
				_ = h.snd.SetUpstreamProxyCA([]byte(previousCA))
				if h.SetUpstreamProxyCA != nil {
					_ = h.SetUpstreamProxyCA([]byte(previousCA))
				}
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := h.setSetting("upstream.proxyCA", upCA); err != nil {
			_ = h.snd.SetUpstreamProxyCA([]byte(previousCA))
			if h.SetUpstreamProxyCA != nil {
				_ = h.SetUpstreamProxyCA([]byte(previousCA))
			}
			httpInternalErr(w, err)
			return
		}
	}
	if up, ok := bundle.Settings["upstream.proxy"]; ok && up != "" {
		if err := h.snd.SetUpstreamProxy(up); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if !hasUpCA && h.Upstream != nil {
			if err := h.Upstream(up); err != nil {
				_ = h.snd.SetUpstreamProxy(previousUp)
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := h.setSetting("upstream.proxy", up); err != nil {
			httpInternalErr(w, err)
			return
		}
	}
	if authz, ok := bundle.Settings["authz.identities"]; ok && authz != "" {
		if err := h.st.SetSetting("authz.identities", authz); err != nil {
			httpInternalErr(w, err)
			return
		}
	}
	if raw, ok := bundle.Settings[originTLSVerifySettingKey]; ok {
		if raw != "0" && raw != "1" {
			httpErr(w, http.StatusBadRequest, "invalid originTLSVerify setting")
			return
		}
		if err := h.setSetting(originTLSVerifySettingKey, raw); err != nil {
			httpInternalErr(w, err)
			return
		}
		if h.SetOriginTLSVerify != nil {
			h.SetOriginTLSVerify(raw == "1")
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if raw, ok := bundle.Settings[originTLSVerifyBypassSettingKey]; ok {
		hosts := parseHostList(raw)
		if err := h.setSetting(originTLSVerifyBypassSettingKey, strings.Join(hosts, "\n")); err != nil {
			httpInternalErr(w, err)
			return
		}
		if h.SetOriginTLSVerifyBypassHosts != nil {
			h.SetOriginTLSVerifyBypassHosts(hosts)
		}
		h.broadcast(map[string]any{"type": "settings.update"})
	}
	if strings.TrimSpace(bundle.Notes) != "" {
		if _, err := h.st.PersistNotes(bundle.Notes); err != nil {
			httpInternalErr(w, err)
			return
		}
		h.broadcast(map[string]any{"type": "notes.update"})
	}

	h.refreshRules()
	h.refreshScope()
	if flows > 0 {
		h.epsCache.invalidate() // imported flows add endpoints — drop the stale Map/endpoints aggregate
		h.broadcast(map[string]any{"type": "flow.new"})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"importedFlows": flows, "importedRules": rulesImported, "importedScope": scopeImported,
	})
}

// apiProject reports the active project and the projects available to switch to.
func (h *projectAPI) apiProject(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"current":   h.ProjectName,
		"dir":       h.ProjectDir,
		"projects":  h.projectEntries(),
		"canSwitch": h.SwitchProject != nil,
	})
}

// projectEntry is one row in the switcher: a named project (Path empty,
// switch via {target: Name}) or a remembered external-folder project (Path
// set, switch via {path: Path}).
type projectEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (h *projectAPI) projectEntries() []projectEntry {
	names := h.availableProjects()
	out := make([]projectEntry, 0, len(names))
	for _, n := range names {
		out = append(out, projectEntry{Name: n})
	}
	for _, e := range readExternalProjects(h.GlobalDir) {
		out = append(out, projectEntry{Name: e.Name, Path: e.Path})
	}
	return out
}

// availableProjects lists "default" plus every named project directory under
// GlobalDir/projects ("default" first, the rest sorted).
func (h *projectAPI) availableProjects() []string {
	out := []string{"default"}
	if h.GlobalDir == "" {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(h.GlobalDir, "projects"))
	if err != nil {
		return out
	}
	var named []string
	for _, e := range entries {
		// "default" is reserved for the root project (already listed first); a
		// like-named subdirectory would otherwise show up twice in the picker.
		if e.IsDir() && !strings.EqualFold(e.Name(), "default") {
			named = append(named, e.Name())
		}
	}
	sort.Strings(named)
	return append(out, named...)
}

// safeProjectTarget reports whether a project target from the network API is a
// bare name safe to hand to the re-exec — never a filesystem path. A path-like
// target (separators, "~", "."/"..") would let a single loopback request
// relocate the running process to an arbitrary directory (MkdirAll + re-exec),
// and a leading "-" could be mis-read as a flag. The local --project CLI flag
// still accepts paths; only the remote switch is restricted to plain names.
func safeProjectTarget(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, "~") && !strings.HasPrefix(name, "-")
}

// switchProject relaunches Interseptor pointed at another project. It answers
// first, then the process re-execs; the UI reconnects once the listeners are
// back. Two mutually exclusive inputs:
//
//   - "target": a plain project name (see safeProjectTarget) — the common
//     case, resolved under GlobalDir/projects. A loopback request can't use
//     this field to relocate the process to an arbitrary path.
//   - "path": an explicit, absolute directory the operator chose (e.g. "save
//     this engagement in D:\clients\acme instead of ~/.interseptor"). This is
//     a deliberate, separate opt-in — the plain "target" field's path
//     restriction is unchanged — and is remembered so it reappears in the
//     switcher on future launches instead of requiring the path to be retyped.
func (h *projectAPI) switchProject(w http.ResponseWriter, r *http.Request) {
	if h.SwitchProject == nil {
		httpErr(w, http.StatusNotImplemented, "project switching unavailable")
		return
	}
	var in struct {
		Target string `json:"target"`
		Path   string `json:"path"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxProjectSwitchRequestBytes, &in) {
		return
	}
	if path := strings.TrimSpace(in.Path); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil || !isSafeExternalPath(abs) {
			httpErr(w, http.StatusBadRequest, "invalid path: use an absolute folder, not a drive/filesystem root")
			return
		}
		name := filepath.Base(abs)
		if err := rememberExternalProject(h.GlobalDir, name, abs); err != nil {
			httpInternalErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"switching": name})
		h.scheduleProjectSwitch(abs, 300*time.Millisecond)
		return
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		httpErr(w, http.StatusBadRequest, "target required")
		return
	}
	if !safeProjectTarget(target) {
		httpErr(w, http.StatusBadRequest, "invalid project: use a plain name, not a path")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"switching": target})
	h.scheduleProjectSwitch(target, 300*time.Millisecond)
}

// scheduleProjectSwitch arms a delayed switch to target after d, canceling any
// pending switch first so rapid repeated requests don't stack delayed re-execs —
// only the latest target actually fires. Guarded by switchMu.
func (h *Hub) scheduleProjectSwitch(target string, d time.Duration) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	if h.switchClosed {
		return
	}
	if h.switchTimer != nil {
		h.switchTimer.Stop()
	}
	h.switchTimer = time.AfterFunc(d, func() {
		h.switchMu.Lock()
		if h.switchClosed {
			h.switchMu.Unlock()
			return
		}
		h.switchTimer = nil
		switchProject := h.SwitchProject
		h.switchWG.Add(1)
		h.switchMu.Unlock()
		defer h.switchWG.Done()
		if switchProject != nil {
			if err := switchProject(target); err != nil {
				log.Printf("control: project switch to %q failed: %v", target, err)
			}
		}
	})
}

func (h *Hub) stopProjectSwitch() {
	h.switchMu.Lock()
	h.switchClosed = true
	if h.switchTimer != nil {
		h.switchTimer.Stop()
		h.switchTimer = nil
	}
	h.switchMu.Unlock()
	h.switchWG.Wait()
}
