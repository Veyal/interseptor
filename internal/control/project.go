package control

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interceptor/internal/harx"
	"github.com/Veyal/interceptor/internal/store"
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

func (h *projectAPI) exportProject(w http.ResponseWriter, r *http.Request) {
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 10000, ExcludeFlags: store.FlagIntruder})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rules, _ := h.st.ListRules()
	scope, _ := h.st.ListScopeRules()
	up, _, _ := h.st.GetSetting("upstream.proxy")
	authz, _, _ := h.st.GetSetting("authz.identities")
	notes, _ := h.st.LoadNotes()
	bundle := projectBundle{
		Version:  "1",
		HAR:      json.RawMessage(harx.Build(flows, h.bodyBytes)),
		Rules:    rules,
		Scope:    scope,
		Notes:    notes,
		Settings: map[string]string{"upstream.proxy": up, "authz.identities": authz},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor-project.json"`)
	json.NewEncoder(w).Encode(bundle)
}

// importProject merges a project into the current session (additive for flows,
// rules, and scope; applies the upstream-proxy setting). It does not rebind the
// proxy listener.
func (h *projectAPI) importProject(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var bundle projectBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		httpErr(w, http.StatusBadRequest, "not a valid project: "+err.Error())
		return
	}

	flows := 0
	if len(bundle.HAR) > 0 {
		if entries, perr := harx.Parse(bundle.HAR); perr == nil {
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
				fl.ReqBodyHash, fl.ReqLen = h.storeBody(e.ReqBody)
				fl.ResBodyHash, fl.ResLen = h.storeBody(e.ResBody)
				if _, err := h.st.InsertFlow(fl); err == nil {
					flows++
				}
			}
		}
	}
	for i := range bundle.Rules {
		bundle.Rules[i].ID = 0
		h.st.CreateRule(&bundle.Rules[i])
	}
	for i := range bundle.Scope {
		bundle.Scope[i].ID = 0
		h.st.CreateScopeRule(&bundle.Scope[i])
	}
	if up, ok := bundle.Settings["upstream.proxy"]; ok && up != "" {
		if h.Upstream != nil {
			_ = h.Upstream(up)
		}
		_ = h.st.SetSetting("upstream.proxy", up)
	}
	if authz, ok := bundle.Settings["authz.identities"]; ok && authz != "" {
		_ = h.st.SetSetting("authz.identities", authz)
	}
	if strings.TrimSpace(bundle.Notes) != "" {
		if _, err := h.st.PersistNotes(bundle.Notes); err == nil {
			h.broadcast(map[string]any{"type": "notes.update"})
		}
	}

	h.refreshRules()
	h.refreshScope()
	if flows > 0 {
		h.epsCache.invalidate() // imported flows add endpoints — drop the stale Map/endpoints aggregate
		h.broadcast(map[string]any{"type": "flow.new"})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"importedFlows": flows, "importedRules": len(bundle.Rules), "importedScope": len(bundle.Scope),
	})
}

// apiProject reports the active project and the projects available to switch to.
func (h *projectAPI) apiProject(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"current":   h.ProjectName,
		"dir":       h.ProjectDir,
		"projects":  h.availableProjects(),
		"canSwitch": h.SwitchProject != nil,
	})
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

// switchProject relaunches Interceptor pointed at another named project. It
// answers first, then the process re-execs; the UI reconnects once the listeners
// are back. The target is restricted to a plain project name (see
// safeProjectTarget) so a loopback request can't relocate the process to an
// arbitrary path.
func (h *projectAPI) switchProject(w http.ResponseWriter, r *http.Request) {
	if h.SwitchProject == nil {
		httpErr(w, http.StatusNotImplemented, "project switching unavailable")
		return
	}
	var in struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
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
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := h.SwitchProject(target); err != nil {
			log.Printf("control: project switch to %q failed: %v", target, err)
		}
	}()
}
