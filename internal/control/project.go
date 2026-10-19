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
	"strconv"
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
	// Collections is the scrubbed collections section (store.CollectionsBundle).
	// Absent in version 1 bundles and when the project has no collections.
	Collections json.RawMessage `json:"collections,omitempty"`
}

// projectBundleVersion is the portable project bundle schema version. Version 2
// added the collections section; version 1 bundles import unchanged.
const projectBundleVersion = 2

// checkProjectBundleVersion rejects bundles written by a newer build while
// tolerating the legacy empty and "1" versions.
func checkProjectBundleVersion(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fmt.Errorf("unsupported project bundle version %q", v)
	}
	if n > projectBundleVersion {
		return fmt.Errorf("project bundle version %d is newer than supported %d", n, projectBundleVersion)
	}
	return nil
}

// exportCollectionsSection returns the scrubbed collections section, or nil
// when the project has none.
func exportCollectionsSection(st *store.Store) (json.RawMessage, error) {
	cb, err := st.ExportCollectionsBundle(store.ScrubOptions{})
	if err != nil {
		return nil, err
	}
	if len(cb.Collections) == 0 && len(cb.Environments) == 0 {
		return nil, nil
	}
	return json.Marshal(cb)
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
	// The portable bundle is meant for sharing: saved proxy passwords are
	// write-only and never leave in it (the proxy-auth password is not exported
	// at all). Import keeps the importer's own password for the same proxy host.
	up, _ = redactUpstreamPassword(up)
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
	collections, err := exportCollectionsSection(h.st)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	bundle := projectBundle{
		Version: strconv.Itoa(projectBundleVersion), Collections: collections, HAR: json.RawMessage(harx.Build(flows, h.bodyBytes)), Rules: rules, Scope: scope, Notes: notes,
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

// localNetworkSettingKeys are this machine's network settings. A portable
// project import never applies them: a bundle from another machine would
// otherwise silently reroute (or break) this instance's traffic. The listener
// bind, proxy authentication and system proxy are not in a bundle at all.
var localNetworkSettingKeys = []string{"upstream.proxy", "upstream.proxyCA"}

// importProject merges a project into the current session. It is additive and
// idempotent for flows (by content signature), match-and-replace rules and
// scope rules (by their fields), so importing the same bundle twice adds
// nothing. Network settings stay local (see localNetworkSettingKeys) and are
// reported in keptLocalSettings when the bundle carried a different value.
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
	if err := checkProjectBundleVersion(bundle.Version); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Decode the collections section up front so a malformed one is rejected
	// before any other part of the bundle is applied.
	collBundle, err := store.DecodeCollectionsBundle(bundle.Collections)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "project contains an invalid collections section: "+err.Error())
		return
	}
	if raw, ok := bundle.Settings[originTLSVerifySettingKey]; ok && raw != "0" && raw != "1" {
		httpErr(w, http.StatusBadRequest, "invalid originTLSVerify setting")
		return
	}

	flows, flowsSkipped := 0, 0
	if harData := bytes.TrimSpace(bundle.HAR); len(harData) > 0 && !bytes.Equal(harData, []byte("null")) {
		entries, err := harx.Parse(harData)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "project contains an invalid HAR: "+err.Error())
			return
		}
		seen, err := h.st.FlowImportSignatures()
		if err != nil {
			httpInternalErr(w, err)
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
			inserted, err := h.insertImportedFlowOnce(fl, e.ReqBody, e.ResBody, seen)
			if err != nil {
				httpInternalErr(w, err)
				return
			}
			if inserted {
				flows++
			} else {
				flowsSkipped++
			}
		}
	}
	existingRules, err := h.st.ListRules()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	ruleSeen := map[string]bool{}
	for _, rl := range existingRules {
		ruleSeen[ruleIdentity(rl)] = true
	}
	rulesImported, rulesSkipped := 0, 0
	for i := range bundle.Rules {
		key := ruleIdentity(bundle.Rules[i])
		if ruleSeen[key] {
			rulesSkipped++
			continue
		}
		bundle.Rules[i].ID = 0
		if _, err := h.st.CreateRule(&bundle.Rules[i]); err != nil {
			httpInternalErr(w, err)
			return
		}
		ruleSeen[key] = true
		rulesImported++
	}
	existingScope, err := h.st.ListScopeRules()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	scopeSeen := map[string]bool{}
	for _, sr := range existingScope {
		scopeSeen[scopeRuleIdentity(sr)] = true
	}
	scopeImported, scopeSkipped := 0, 0
	for i := range bundle.Scope {
		key := scopeRuleIdentity(bundle.Scope[i])
		if scopeSeen[key] {
			scopeSkipped++
			continue
		}
		bundle.Scope[i].ID = 0
		if _, err := h.st.CreateScopeRule(&bundle.Scope[i]); err != nil {
			httpInternalErr(w, err)
			return
		}
		scopeSeen[key] = true
		scopeImported++
	}
	keptLocal, err := h.keptLocalNetworkSettings(bundle.Settings)
	if err != nil {
		httpInternalErr(w, err)
		return
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
	notesOutcome, err := h.importNotes(bundle.Notes)
	if err != nil {
		httpInternalErr(w, err)
		return
	}

	collStats, err := h.st.ImportCollectionsBundle(collBundle)
	if err != nil {
		httpInternalErr(w, err)
		return
	}

	h.refreshRules()
	h.refreshScope()
	if flows > 0 {
		h.epsCache.invalidate() // imported flows add endpoints — drop the stale Map/endpoints aggregate
		h.broadcast(map[string]any{"type": "flow.new"})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"importedFlows": flows, "importedRules": rulesImported, "importedScope": scopeImported,
		"skippedFlows": flowsSkipped, "skippedRules": rulesSkipped, "skippedScope": scopeSkipped,
		"importedCollections": collStats, "keptLocalSettings": keptLocal, "notes": notesOutcome,
	})
}

// Notes import outcomes reported as "notes" in the import result.
const (
	notesNone     = ""         // the bundle carried no notes
	notesImported = "imported" // local notes were empty; the bundle's were taken
	notesKept     = "kept"     // the bundle's notes were identical or already contained locally
	notesMerged   = "merged"   // local notes kept, the bundle's appended under a heading
)

// mergeImportedNotes never discards local notes: empty local takes the
// incoming text, identical or already-contained incoming leaves local alone
// (so re-importing a bundle is idempotent), anything else is appended under an
// "Imported from" heading.
func mergeImportedNotes(local, incoming string, now time.Time) (string, string) {
	inc := strings.TrimSpace(incoming)
	if inc == "" {
		return local, notesNone
	}
	if strings.TrimSpace(local) == "" {
		return incoming, notesImported
	}
	if strings.Contains(local, inc) {
		return local, notesKept
	}
	return strings.TrimRight(local, "\n") + "\n\n## Imported from project export (" + now.Format("2006-01-02") + ")\n\n" + inc + "\n", notesMerged
}

// importNotes applies mergeImportedNotes to the project notes.
func (h *projectAPI) importNotes(incoming string) (string, error) {
	if strings.TrimSpace(incoming) == "" {
		return notesNone, nil
	}
	local, err := h.st.LoadNotes()
	if err != nil {
		return "", err
	}
	merged, outcome := mergeImportedNotes(local, incoming, time.Now())
	if merged != local {
		if _, err := h.st.PersistNotes(merged); err != nil {
			return "", err
		}
		h.broadcast(map[string]any{"type": "notes.update"})
	}
	return outcome, nil
}

// keptLocalNetworkSettings lists the network settings the bundle carried with
// a value different from this machine's; import leaves them unchanged. Saved
// passwords never travel, so the upstream URL is compared without one.
func (h *projectAPI) keptLocalNetworkSettings(settings map[string]string) ([]string, error) {
	kept := []string{}
	for _, key := range localNetworkSettingKeys {
		incoming, ok := settings[key]
		if !ok {
			continue
		}
		local, _, err := h.st.GetSetting(key)
		if err != nil {
			return nil, err
		}
		incoming, local = strings.TrimSpace(incoming), strings.TrimSpace(local)
		if key == "upstream.proxy" {
			incoming, _ = redactUpstreamPassword(incoming)
			local, _ = redactUpstreamPassword(local)
		}
		if incoming != local {
			kept = append(kept, key)
		}
	}
	return kept, nil
}

// ruleIdentity is a match-and-replace rule's content, without id and order.
func ruleIdentity(r store.Rule) string {
	return fmt.Sprintf("%t\x00%s\x00%s\x00%s\x00%t", r.Enabled, r.Type, r.Match, r.Replace, r.BigBody)
}

// scopeRuleIdentity is a scope rule's content, without id and order; the
// scheme and host compare case-insensitively like the matcher (paths keep
// their case).
func scopeRuleIdentity(r store.ScopeRule) string {
	return fmt.Sprintf("%t\x00%s\x00%s\x00%s\x00%s\x00%d", r.Enabled, r.Action, strings.ToLower(r.Host), r.Path, strings.ToLower(r.Scheme), r.Port)
}

// apiProject reports the active project and the projects available to switch to.
func (h *projectAPI) apiProject(w http.ResponseWriter, r *http.Request) {
	h.rememberCurrentProjectOpen()
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
	Name      string `json:"name"`
	Path      string `json:"path"`
	Category  string `json:"category,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	OpenedAt  int64  `json:"openedAt,omitempty"`
}

func (h *projectAPI) projectEntries() []projectEntry {
	type spec struct {
		name, path, key, dir string
	}
	var specs []spec
	for _, n := range h.availableProjects() {
		specs = append(specs, spec{name: n, key: projectMetaKey(n, ""), dir: h.namedProjectDir(n)})
	}
	for _, e := range readExternalProjects(h.GlobalDir) {
		specs = append(specs, spec{name: e.Name, path: e.Path, key: projectMetaKey("", e.Path), dir: e.Path})
	}
	dirs := make(map[string]string, len(specs))
	for _, s := range specs {
		dirs[s.key] = s.dir
	}
	index := ensureProjectCreated(h.GlobalDir, dirs)
	out := make([]projectEntry, 0, len(specs))
	for _, s := range specs {
		meta := index[s.key]
		out = append(out, projectEntry{
			Name: s.name, Path: s.path, Category: meta.Category,
			CreatedAt: meta.CreatedAt, OpenedAt: meta.OpenedAt,
		})
	}
	return out
}

func (h *projectAPI) namedProjectDir(name string) string {
	if h.GlobalDir == "" {
		return ""
	}
	if name == "" || strings.EqualFold(name, "default") {
		return h.GlobalDir
	}
	return filepath.Join(h.GlobalDir, "projects", name)
}

func (h *Hub) rememberCurrentProjectOpen() {
	if h.GlobalDir == "" || !h.projectOpenNoted.CompareAndSwap(false, true) {
		return
	}
	name := h.ProjectName
	if name == "" {
		name = "default"
	}
	dir := h.ProjectDir
	key := projectMetaKey(name, "")
	if dir != "" {
		named := filepath.Join(h.GlobalDir, "projects", name)
		clean := filepath.Clean(dir)
		if !strings.EqualFold(name, "default") && clean != filepath.Clean(named) && clean != filepath.Clean(h.GlobalDir) {
			key = projectMetaKey("", clean)
		}
	} else if strings.EqualFold(name, "default") {
		dir = h.GlobalDir
	} else {
		dir = filepath.Join(h.GlobalDir, "projects", name)
	}
	_ = touchProjectMeta(h.GlobalDir, key, dir, true)
}

func (h *projectAPI) setProjectFolder(w http.ResponseWriter, r *http.Request) {
	if h.GlobalDir == "" {
		httpErr(w, http.StatusBadRequest, "project storage location is not configured")
		return
	}
	var in struct {
		Target   string `json:"target"`
		Path     string `json:"path"`
		Category string `json:"category"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxProjectSwitchRequestBytes, &in) {
		return
	}
	category, err := normalizeProjectCategory(in.Category)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var key string
	if path := strings.TrimSpace(in.Path); path != "" {
		if strings.TrimSpace(in.Target) != "" {
			httpErr(w, http.StatusBadRequest, "set a project name or a folder path, not both")
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil || !isSafeExternalPath(abs) {
			httpErr(w, http.StatusBadRequest, "invalid path: use an absolute folder, not a drive/filesystem root")
			return
		}
		key = projectMetaKey("", abs)
	} else {
		target := strings.TrimSpace(in.Target)
		if !safeProjectTarget(target) {
			httpErr(w, http.StatusBadRequest, "invalid project: use a plain name, not a path")
			return
		}
		key = projectMetaKey(target, "")
	}
	if err := setProjectCategory(h.GlobalDir, key, category); err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"category": category})
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
		// The restarted process exits when it cannot open this folder, which
		// would take the whole instance down. Prove it is usable first.
		if err := ensureUsableProjectDir(abs); err != nil {
			httpErr(w, http.StatusBadRequest, "cannot use that folder: "+err.Error())
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
