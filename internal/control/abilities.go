package control

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/abilities"
)

const (
	maxAbilityArchiveBytes int64 = 32 << 20 // 32 MiB compressed upload cap
	maxAbilityJSONBytes    int64 = 1 << 20  // 1 MiB JSON control/action payloads
)

type abilitiesAPI struct{ h *Hub }

// ConfigureAbilities initializes the Hub's Copy Abilities manager across global
// and project directories, wires SSE change notifications, and registers its
// asynchronous flow.captured Starlark hook once.
func (h *Hub) ConfigureAbilities(globalDir, projectDir string) error {
	mgr, err := abilities.NewManager(abilities.Config{
		GlobalDir:  globalDir,
		ProjectDir: projectDir,
		Store:      h.st,
		OnChange:   func() { h.broadcast(map[string]any{"type": "ability.update"}) },
	})
	if err != nil {
		return err
	}
	mgr.RegisterPluginHook()

	h.abilitiesMu.Lock()
	prev := h.abilities
	h.abilities = mgr
	h.abilitiesMu.Unlock()
	if prev != nil {
		prev.Close()
	}
	return nil
}

func (h *Hub) closeAbilities() {
	h.abilitiesMu.Lock()
	mgr := h.abilities
	h.abilities = nil
	h.abilitiesMu.Unlock()
	if mgr != nil {
		mgr.Close()
	}
}

// ensureAbilities lazily initializes an empty-dir or Hub-dir abilities manager
// when ConfigureAbilities was not explicitly called (such as unit tests).
func (h *Hub) ensureAbilities() (*abilities.Manager, error) {
	h.abilitiesMu.Lock()
	defer h.abilitiesMu.Unlock()

	if h.abilities != nil {
		return h.abilities, nil
	}
	mgr, err := abilities.NewManager(abilities.Config{
		GlobalDir:  h.GlobalDir,
		ProjectDir: h.ProjectDir,
		Store:      h.st,
		OnChange:   func() { h.broadcast(map[string]any{"type": "ability.update"}) },
	})
	if err != nil {
		return nil, err
	}
	if h.GlobalDir != "" || h.ProjectDir != "" {
		mgr.RegisterPluginHook()
	}
	h.abilities = mgr
	return mgr, nil
}

func (h *Hub) abilitiesManager() (*abilities.Manager, error) {
	return h.ensureAbilities()
}

func (a *abilitiesAPI) managerOrErr(w http.ResponseWriter) (*abilities.Manager, bool) {
	mgr, err := a.h.ensureAbilities()
	if err != nil {
		httpInternalErr(w, err)
		return nil, false
	}
	return mgr, true
}

func (a *abilitiesAPI) list(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"abilities":             mgr.List(),
		"projectScopeAvailable": mgr.HasProjectScope(),
	})
}

func (a *abilitiesAPI) get(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	ab, found := mgr.Get(r.PathValue("id"))
	if !found {
		httpErr(w, http.StatusNotFound, "ability not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) install(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}

	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope != "" && scope != abilities.ScopeGlobal && scope != abilities.ScopeProject {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("invalid ability scope %q (want %q or %q)", scope, abilities.ScopeGlobal, abilities.ScopeProject))
		return
	}

	mediaType, _, ctErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ctErr == nil && mediaType == "multipart/form-data" {
		a.installMultipart(w, r, mgr, scope)
		return
	}

	if scope == "" {
		scope = abilities.ScopeGlobal
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAbilityUploadBytes)
	ab, err := mgr.InstallArchive(r.Body, maxAbilityArchiveBytes, scope)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) installMultipart(w http.ResponseWriter, r *http.Request, mgr *abilities.Manager, scope string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAbilityUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if isBodyTooLarge(err) {
			httpErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("ability archive exceeds %d bytes", maxAbilityArchiveBytes))
			return
		}
		httpErr(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	if scope == "" {
		scope = strings.TrimSpace(r.FormValue("scope"))
	}
	if scope == "" {
		scope = abilities.ScopeGlobal
	}
	if scope != abilities.ScopeGlobal && scope != abilities.ScopeProject {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("invalid ability scope %q (want %q or %q)", scope, abilities.ScopeGlobal, abilities.ScopeProject))
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httpErr(w, http.StatusBadRequest, `missing multipart file field "file"`)
		return
	}
	defer file.Close()

	ab, err := mgr.InstallArchive(file, maxAbilityArchiveBytes, scope)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) scaffold(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}

	var req abilities.ScaffoldRequest
	if !decodeLimitedJSON(w, r, maxAbilityJSONBytes, &req) {
		return
	}
	ab, err := mgr.Scaffold(req)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) reload(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	var unused map[string]any
	if !decodeOptionalLimitedJSON(w, r, maxAbilityJSONBytes, &unused) {
		return
	}
	if err := mgr.Reload(); err != nil {
		httpInternalErr(w, err)
		return
	}
	a.h.broadcast(map[string]any{"type": "ability.update"})
	writeJSON(w, http.StatusOK, map[string]any{"abilities": mgr.List()})
}

func (a *abilitiesAPI) trust(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}

	var in struct {
		Digest  string `json:"digest"`
		Scope   string `json:"scope"`
		Enabled *bool  `json:"enabled"`
	}
	if !decodeLimitedJSON(w, r, maxAbilityJSONBytes, &in) {
		return
	}
	if in.Digest == "" || (in.Scope != abilities.ScopeGlobal && in.Scope != abilities.ScopeProject) {
		httpErr(w, http.StatusBadRequest, "reviewed digest and scope are required")
		return
	}

	ab, err := mgr.Approve(r.PathValue("id"), in.Digest, in.Scope, in.Enabled == nil || *in.Enabled)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) revoke(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}

	var unused map[string]any
	if !decodeOptionalLimitedJSON(w, r, maxAbilityJSONBytes, &unused) {
		return
	}
	ab, err := mgr.RevokeTrust(r.PathValue("id"))
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) toggle(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}

	var in struct {
		Enabled *bool  `json:"enabled"`
		Digest  string `json:"digest"`
		Scope   string `json:"scope"`
	}
	if !decodeLimitedJSON(w, r, maxAbilityJSONBytes, &in) {
		return
	}
	if in.Enabled == nil {
		httpErr(w, http.StatusBadRequest, "enabled boolean is required")
		return
	}

	ab, err := mgr.SetEnabledChecked(r.PathValue("id"), *in.Enabled, in.Digest, in.Scope)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ability": ab})
}

func (a *abilitiesAPI) runAction(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	input, ok := decodeAbilityActionInput(w, r)
	if !ok {
		return
	}

	res, err := mgr.RunActionForCaller(r.Context(), r.PathValue("id"), r.PathValue("action"), input, requireUISession(r) == nil)
	if err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// decodeAbilityActionInput decodes an optional JSON object body using json.Number
// so Starlark actions receive exact int64 numbers instead of float64.
func decodeAbilityActionInput(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAbilityJSONBytes))
	dec.UseNumber()

	var input map[string]any
	if err := dec.Decode(&input); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, true
		}
		writeJSONDecodeError(w, err)
		return nil, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			httpErr(w, http.StatusBadRequest, "bad json")
		} else {
			writeJSONDecodeError(w, err)
		}
		return nil, false
	}
	if input == nil {
		input = map[string]any{}
	}
	return input, true
}

func (a *abilitiesAPI) export(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	ab, found := mgr.Get(id)
	if !found {
		httpErr(w, http.StatusNotFound, "ability not found")
		return
	}

	lw := &lazyArchiveWriter{
		w:        w,
		filename: safeAbilityArchiveName(ab.ID),
	}
	if err := mgr.ExportArchive(id, lw); err != nil {
		if !lw.wrote {
			abilityErr(w, err)
		}
		return
	}
}

func (a *abilitiesAPI) remove(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	digest := strings.TrimSpace(r.URL.Query().Get("digest"))
	if err := mgr.RemoveChecked(id, scope, digest); err != nil {
		abilityErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (a *abilitiesAPI) serveAsset(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.managerOrErr(w)
	if !ok {
		return
	}
	asset := r.PathValue("asset")
	data, contentType, ok := mgr.ReadAsset(r.PathValue("id"), asset, r.PathValue("revision"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path.Base(asset), time.Time{}, bytes.NewReader(data))
}

type lazyArchiveWriter struct {
	w        http.ResponseWriter
	filename string
	wrote    bool
}

func (l *lazyArchiveWriter) Write(p []byte) (int, error) {
	if !l.wrote {
		l.wrote = true
		l.w.Header().Set("Content-Type", "application/gzip")
		l.w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, l.filename))
		l.w.Header().Set("X-Content-Type-Options", "nosniff")
		l.w.WriteHeader(http.StatusOK)
	}
	return l.w.Write(p)
}

func safeAbilityArchiveName(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		name = "ability"
	}
	return name + ".tar.gz"
}

func abilityErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, abilities.ErrNotFound):
		httpErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, abilities.ErrAlreadyExists):
		httpErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, abilities.ErrDrift):
		httpErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, abilities.ErrNotTrusted):
		httpErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, abilities.ErrActionNotExposed):
		httpErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, abilities.ErrInvalidManifest):
		httpErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, abilities.ErrInvalidArchive):
		if strings.Contains(err.Error(), "exceeds limit") {
			httpErr(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		httpErr(w, http.StatusNotFound, "flow not found")
	default:
		msg := err.Error()
		switch {
		case strings.Contains(msg, " not found in ability "):
			httpErr(w, http.StatusNotFound, msg)
		case strings.HasPrefix(msg, "cannot trust ability "),
			strings.HasPrefix(msg, "cannot enable ability "),
			strings.Contains(msg, " has load error"):
			httpErr(w, http.StatusConflict, msg)
		case isAbilityValidationOrScriptErr(msg):
			httpErr(w, http.StatusBadRequest, msg)
		default:
			httpInternalErr(w, err)
		}
	}
}

func isAbilityValidationOrScriptErr(msg string) bool {
	for _, marker := range []string{
		"invalid ability scope",
		"is not configured",
		"no ability scope directory configured",
		"project ability scope is unavailable",
		"must not be a symlink",
		"must be a regular directory",
		"manifest ",
		"referenced file ",
		"ability exceeds ",
		" exceeds ",
		"symlink ",
		"non-regular file ",
		"action input:",
		"convert action result:",
		"starlark ",
		"compile ",
		"missing ",
		"must be a function",
		"panicked:",
		"action cancelled",
		"hook ",
		"action ",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
